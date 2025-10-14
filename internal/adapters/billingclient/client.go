package billingclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"tg-digest-bot/internal/domain"
)

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		if client != nil {
			c.httpClient = client
		}
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		if c.httpClient == nil {
			c.httpClient = &http.Client{}
		}
		c.httpClient.Timeout = timeout
	}
}

type apiError struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func New(baseURL string, opts ...Option) (*Client, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("baseURL is required")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "http"
	}
	client := &Client{
		baseURL:    parsed,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
	for _, opt := range opts {
		opt(client)
	}
	return client, nil
}

func (c *Client) EnsureAccount(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	payload := map[string]any{"user_id": userID}
	var account domain.BillingAccount
	if err := c.post(ctx, "/api/v1/accounts/ensure", payload, &account); err != nil {
		return domain.BillingAccount{}, err
	}
	return account, nil
}

func (c *Client) GetAccountByUserID(ctx context.Context, userID int64) (domain.BillingAccount, error) {
	var account domain.BillingAccount
	endpoint := fmt.Sprintf("/api/v1/accounts/by-user/%d", userID)
	if err := c.get(ctx, endpoint, &account); err != nil {
		return domain.BillingAccount{}, err
	}
	return account, nil
}

func (c *Client) CreateInvoice(ctx context.Context, params domain.CreateInvoiceParams) (domain.Invoice, error) {
	var invoice domain.Invoice
	if err := c.post(ctx, "/api/v1/invoices", params, &invoice); err != nil {
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (c *Client) RegisterIncomingPayment(ctx context.Context, params domain.RegisterIncomingPaymentParams) (domain.Payment, error) {
	var payment domain.Payment
	if err := c.post(ctx, "/api/v1/payments/incoming", params, &payment); err != nil {
		return domain.Payment{}, err
	}
	return payment, nil
}

func (c *Client) GetInvoiceByID(ctx context.Context, invoiceID int64) (domain.Invoice, error) {
	var invoice domain.Invoice
	endpoint := fmt.Sprintf("/api/v1/invoices/%d", invoiceID)
	if err := c.get(ctx, endpoint, &invoice); err != nil {
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (c *Client) GetInvoiceByIdempotencyKey(ctx context.Context, key string) (domain.Invoice, error) {
	var invoice domain.Invoice
	endpoint := fmt.Sprintf("/api/v1/invoices/idempotency/%s", url.PathEscape(key))
	if err := c.get(ctx, endpoint, &invoice); err != nil {
		return domain.Invoice{}, err
	}
	return invoice, nil
}

func (c *Client) ChargeAccount(ctx context.Context, params domain.ChargeAccountParams) (domain.Payment, error) {
	var payment domain.Payment
	if err := c.post(ctx, "/api/v1/accounts/charge", params, &payment); err != nil {
		return domain.Payment{}, err
	}
	return payment, nil
}

func (c *Client) CreateInvoiceWithQRCode(ctx context.Context, params domain.CreateSBPInvoiceParams) (domain.CreateSBPInvoiceResult, error) {
	payload := map[string]any{
		"user_id":          params.UserID,
		"amount_minor":     params.Amount.Amount,
		"currency":         params.Amount.Currency,
		"description":      params.Description,
		"payment_purpose":  params.PaymentPurpose,
		"idempotency_key":  params.IdempotencyKey,
		"order_id":         params.OrderID,
		"qr_type":          params.QRType,
		"notification_url": params.NotificationURL,
		"metadata":         params.Metadata,
		"extra":            params.Extra,
	}
	if params.Metadata == nil {
		delete(payload, "metadata")
	}
	if params.Extra == nil {
		delete(payload, "extra")
	}
	var result domain.CreateSBPInvoiceResult
	if err := c.post(ctx, "/api/v1/sbp/invoices", payload, &result); err != nil {
		return domain.CreateSBPInvoiceResult{}, err
	}
	return result, nil
}

func (c *Client) get(ctx context.Context, endpoint string, out any) error {
	req, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) post(ctx context.Context, endpoint string, body any, out any) error {
	req, err := c.newRequest(ctx, http.MethodPost, endpoint, body)
	if err != nil {
		return err
	}
	return c.do(req, out)
}

func (c *Client) newRequest(ctx context.Context, method, endpoint string, body any) (*http.Request, error) {
	resolved := *c.baseURL
	basePath := strings.TrimSuffix(c.baseURL.Path, "/")
	resolved.Path = path.Clean(basePath + endpoint)
	if !strings.HasSuffix(endpoint, "/") && strings.HasSuffix(resolved.Path, "/") {
		resolved.Path = strings.TrimSuffix(resolved.Path, "/")
	}
	var buf io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		buf = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, resolved.String(), buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

func (c *Client) do(req *http.Request, out any) error {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("billing api request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		var apiErr apiError
		data, readErr := io.ReadAll(resp.Body)
		if readErr == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &apiErr)
		}
		if apiErr.Error == "" {
			apiErr.Error = strings.TrimSpace(string(data))
		}
		return mapAPIError(resp.StatusCode, apiErr)
	}

	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func mapAPIError(status int, err apiError) error {
	switch err.Code {
	case "invoice_not_found":
		return domain.ErrInvoiceNotFound
	case "account_not_found":
		return domain.ErrAccountNotFound
	case "insufficient_funds":
		return domain.ErrInsufficientFunds
	case "invalid_request":
		return fmt.Errorf("billing api invalid request: %s", err.Error)
	case "":
		return fmt.Errorf("billing api error: status=%d message=%s", status, err.Error)
	default:
		return fmt.Errorf("billing api error [%s]: %s", err.Code, err.Error)
	}
}

var _ domain.Billing = (*Client)(nil)
var _ domain.BillingSBP = (*Client)(nil)
