package tochka

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"billing/internal/domain"
)

type Config struct {
	BaseURL     string
	MerchantID  string
	AccountID   string
	AccessToken string
	Timeout     time.Duration
}

type Client struct {
	cfg        Config
	httpClient *http.Client
}

func NewClient(cfg Config) *Client {
	client := &Client{cfg: cfg}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	client.httpClient = &http.Client{Timeout: timeout}
	if cfg.BaseURL == "" {
		client.cfg.BaseURL = "https://enter.tochka.com"
	}
	return client
}

func (c *Client) SetHTTPClient(httpClient *http.Client) {
	if httpClient != nil {
		c.httpClient = httpClient
	}
}

type RegisterQRCodeRequest struct {
	Amount          domain.Money
	Description     string
	PaymentPurpose  string
	QRType          string
	IdempotencyKey  string
	NotificationURL string
	Extra           map[string]any
}

type RegisterQRCodeResponse struct {
	QRID          string         `json:"qr_id"`
	PaymentLink   string         `json:"payment_link,omitempty"`
	Payload       string         `json:"payload,omitempty"`
	PayloadBase64 string         `json:"payload_base64,omitempty"`
	Status        string         `json:"status,omitempty"`
	ExpiresAt     *time.Time     `json:"expires_at,omitempty"`
	Raw           map[string]any `json:"raw,omitempty"`
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

	payload := make(map[string]any, len(req.Extra)+4)
	for k, v := range req.Extra {
		payload[k] = v
	}

	if _, ok := payload["amount"]; !ok {
		payload["amount"] = map[string]any{
			"value":    formatMinorAmount(req.Amount.Amount),
			"currency": currency,
		}
	}
	if req.Description != "" {
		payload["description"] = req.Description
	}
	if req.PaymentPurpose != "" {
		payload["paymentPurpose"] = req.PaymentPurpose
	}
	if req.QRType != "" {
		payload["qrType"] = req.QRType
	}
	if req.NotificationURL != "" {
		payload["notificationUrl"] = req.NotificationURL
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("marshal request: %w", err)
	}

	baseURL := strings.TrimRight(c.cfg.BaseURL, "/")
	endpoint := fmt.Sprintf("%s/qr-code/merchant/%s/account/%s", baseURL, url.PathEscape(c.cfg.MerchantID), url.PathEscape(c.cfg.AccountID))
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Idempotency-Key", req.IdempotencyKey)
	httpReq.Header.Set("X-Idempotency-Key", req.IdempotencyKey)
	httpReq.Header.Set("X-Request-ID", req.IdempotencyKey)
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
		QRID          string `json:"qrId"`
		PaymentLink   string `json:"paymentLink"`
		Payload       string `json:"payload"`
		PayloadBase64 string `json:"payloadBase64"`
		Status        string `json:"status"`
		ExpiresAt     string `json:"qrExpirationDate"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return RegisterQRCodeResponse{}, fmt.Errorf("decode response: %w", err)
	}
	var raw map[string]any
	_ = json.Unmarshal(data, &raw)

	respData := RegisterQRCodeResponse{
		QRID:          parsed.QRID,
		PaymentLink:   parsed.PaymentLink,
		Payload:       parsed.Payload,
		PayloadBase64: parsed.PayloadBase64,
		Status:        parsed.Status,
		Raw:           raw,
	}
	if parsed.ExpiresAt != "" {
		if ts := parseTime(parsed.ExpiresAt); ts != nil {
			respData.ExpiresAt = ts
		}
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
