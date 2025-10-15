package tochka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"billing/internal/domain"
)

const (
	qrType      = "02"
	sourceName  = "Telegram Digest Bot"
	redirectUrl = "https://t.me/news_collector_dev_bot"
	ttl         = 600 // seconds
)

type Client struct {
	cfg        Config
	httpClient *http.Client
}

// tochka/client.go

type Config struct {
	BaseURL     string
	APIVersion  string // NEW: e.g. "v1.0"
	MerchantID  string
	AccountID   string // bank account number (20 digits)
	AccessToken string
	Timeout     time.Duration
}

func NewClient(cfg Config) *Client {
	client := &Client{cfg: cfg}
	if client.cfg.BaseURL == "" {
		client.cfg.BaseURL = "https://enter.tochka.com"
	}
	if client.cfg.APIVersion == "" {
		client.cfg.APIVersion = "v1.0"
	}

	if len(cfg.AccountID) != 20 {
		log.Warn().Msg("account ID should be 20 digits")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client.httpClient = &http.Client{Timeout: timeout}
	return client
}

func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.httpClient = httpClient
	}
}

type RegisterQRCodeRequest struct {
	Amount         domain.Money
	Description    string
	PaymentPurpose string
	QRType         string
	IdempotencyKey string
	RedirectUrl    string
	Extra          map[string]any
}

type registerQrCodeData struct {
	Amount         int64  `json:"amount"`
	Currency       string `json:"currency"`
	PaymentPurpose string `json:"paymentPurpose"`
	QrcType        string `json:"qrcType"`
	ImageParams    struct {
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		MediaType string `json:"mediaType"`
	} `json:"imageParams"`
	SourceName  string `json:"sourceName"`
	Ttl         int    `json:"ttl"`
	RedirectUrl string `json:"redirectUrl"`
}

type RegisterQRCodeDto struct {
	Data registerQrCodeData `json:"Data"`
}

type RegisterQRCodeResponse struct {
	QRID        string         `json:"qr_id"`
	PaymentLink string         `json:"payment_link,omitempty"`
	Raw         map[string]any `json:"raw,omitempty"`
	ExpiresAt   *time.Time     `json:"expires_at"`
}

func (c *Client) RegisterQRCode(ctx context.Context, req RegisterQRCodeRequest) (RegisterQRCodeResponse, error) {
	if req.IdempotencyKey == "" {
		return RegisterQRCodeResponse{}, fmt.Errorf("idempotency key is required")
	}
	if c.httpClient == nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("http client is not configured")
	}
	currency := req.Amount.Currency
	if currency == "" {
		currency = "RUB"
	}

	payload := RegisterQRCodeDto{
		Data: registerQrCodeData{
			Amount:         req.Amount.Amount,
			Currency:       req.Amount.Currency,
			PaymentPurpose: req.PaymentPurpose,
			QrcType:        qrType,
			ImageParams: struct {
				Width     int    `json:"width"`
				Height    int    `json:"height"`
				MediaType string `json:"mediaType"`
			}{
				Width:     200,
				Height:    200,
				MediaType: "image/png",
			},
			SourceName:  sourceName,
			Ttl:         ttl,
			RedirectUrl: redirectUrl,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	base := strings.TrimRight(c.cfg.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/uapi/sbp/%s/qr-code/merchant/%s/%s",
		base,
		url.PathEscape(c.cfg.APIVersion),
		url.PathEscape(c.cfg.MerchantID),
		url.PathEscape(c.cfg.AccountID),
	)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey) // оставить один
	if c.cfg.AccessToken != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return RegisterQRCodeResponse{}, fmt.Errorf("tochka register qr failed: %s", strings.TrimSpace(string(data)))
	}

	var parsed struct {
		Data struct {
			Payload string `json:"payload"`
			QrcId   string `json:"qrcId"`
			Image   struct {
				Width     int    `json:"width"`
				Height    int    `json:"height"`
				MediaType string `json:"mediaType"`
				Content   string `json:"content"`
			} `json:"image"`
		} `json:"Data"`
		Links struct {
			Self string `json:"self"`
		} `json:"Links"`
		Meta struct {
			TotalPages int `json:"totalPages"`
		} `json:"Meta"`
	}

	if err := json.Unmarshal(data, &parsed); err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("decode response: %w", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)

	expiresAt := time.Now().Add(time.Second * ttl)

	respData := RegisterQRCodeResponse{
		QRID:        parsed.Data.QrcId,
		PaymentLink: parsed.Data.Payload,
		Raw:         raw,
		ExpiresAt:   &expiresAt,
	}
	return respData, nil
}

func formatMinorAmount(amount int64) string {
	negative := amount < 0
	if negative {
		amount = -amount
	}
	major := amount / 100
	minor := amount % 100
	formatted := fmt.Sprintf("%d.%02d", major, minor)
	if negative {
		return "-" + formatted
	}
	return formatted
}

func parseTime(value string) *time.Time {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05-07:00",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, value); err == nil {
			return &t
		}
	}
	return nil
}
