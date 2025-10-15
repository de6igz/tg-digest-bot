package tochka

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ===== Ошибки

var (
	ErrInvalidWebhookSignature = errors.New("tochka: invalid webhook signature")
	ErrUnsupportedJWTAlg       = errors.New("tochka: unsupported jwt alg (expected RS256)")
	ErrEmptyPayload            = errors.New("tochka: empty webhook payload")
)

// ===== Ваши типы (имеющийся IncomingPaymentNotification используем как есть)

type IncomingPaymentNotification struct {
	Event          string
	ID             string
	OrderID        string
	PaymentID      string
	QRID           string
	Status         string
	PaymentPurpose string
	PaymentDate    *time.Time
	Amount         Amount
	PayerName      string
	PayerINN       string
	PayerAccount   string
	PayerBankName  string
	Payload        map[string]any
	Raw            map[string]any
}

// Amount — ваш тип. Здесь не лезем в его формат,
// лишь предлагаем хук-конвертер ниже (см. AmountFromString).

// ===== Вспомогательные структуры для распаковки payload

type sbpPayload struct {
	OperationID       string `json:"operationId"`
	QRID              string `json:"qrcId"`
	AmountStr         string `json:"amount"`
	PayerMobileNumber string `json:"payerMobileNumber"` // игнорируем в итоговом объекте
	PayerName         string `json:"payerName"`
	BrandName         string `json:"brandName"`
	MerchantID        string `json:"merchantId"`
	Purpose           string `json:"purpose"`
	WebhookType       string `json:"webhookType"`
	CustomerCode      string `json:"customerCode"`
	RefTransactionID  string `json:"refTransactionId"`
	OrderID           string `json:"orderId,omitempty"`
	PaymentID         string `json:"paymentId,omitempty"`
	PayerINN          string `json:"payerInn,omitempty"`
	PayerAccount      string `json:"payerAccount,omitempty"`
	PayerBankName     string `json:"payerBankName,omitempty"`
	Status            string `json:"status,omitempty"`
	PaymentDateStr    string `json:"paymentDate,omitempty"`
	// Любые иные поля:
	Rest json.RawMessage `json:"-"`
}

type jwtHeader struct {
	Typ string `json:"typ"`
	Alg string `json:"alg"`
}

type jwtEnvelope struct {
	Header    json.RawMessage `json:"header"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}

// ===== Хук-конвертер суммы (адаптируйте под свой Amount).
// По умолчанию — no-op (не трогаем Amount).
//
// Если у вас есть конструктор вроде NewAmountFromString("0.33"),
// замените реализацию ниже.
var AmountFromString = func(s string) (Amount, error) {
	var a Amount
	a.Value = s
	// no-op: оставьте нулевым, если не готовы мапить здесь.
	// Пример (раскомментируйте и адаптируйте):
	// return NewAmountFromString(s) // ваш конструктор
	return a, nil
}

// ===== Публичная точка входа

// ParseSbpWebhook разбирает входной webhook body в одном из форматов:
// 1) компактный JWT "a.b.c"
// 2) JSON-envelope {"header":{...}, "payload":{...}, "signature":"..."}
// 3) "голый" JSON payload без подписи
// Если key != nil, проверяется RS256-подпись (для 1 и 2 форматов).
func ParseSbpWebhook(body []byte, key *rsa.PublicKey) (IncomingPaymentNotification, error) {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return IncomingPaymentNotification{}, ErrEmptyPayload
	}

	// Формат 1: компактный JWT
	if isCompactJWT(body) {
		headerB64, payloadB64, sigB64 := splitCompactJWT(string(body))
		headerBytes, err := b64urlOrStdDecode(headerB64)
		if err != nil {
			return IncomingPaymentNotification{}, fmt.Errorf("decode compact header: %w", err)
		}
		payloadBytes, err := b64urlOrStdDecode(payloadB64)
		if err != nil {
			return IncomingPaymentNotification{}, fmt.Errorf("decode compact payload: %w", err)
		}
		sigBytes, err := b64urlOrStdDecode(sigB64)
		if err != nil {
			return IncomingPaymentNotification{}, fmt.Errorf("decode compact signature: %w", err)
		}

		if key != nil {
			if err := verifyRS256(headerBytes, payloadBytes, sigBytes, key); err != nil {
				return IncomingPaymentNotification{}, err
			}
		}

		return buildNotificationFromPayload(payloadBytes, map[string]any{
			"format":    "compact-jwt",
			"headerRaw": jsonRawOrNil(headerBytes),
		})
	}

	// Формат 2: JSON-envelope (header/payload/signature)
	if body[0] == '{' {
		// Попробуем как envelope:
		var env jwtEnvelope
		if json.Unmarshal(body, &env) == nil && len(bytes.TrimSpace(env.Payload)) > 0 {
			payloadBytes, headerBytes, sigBytes, hasSig, err := decodeEnvelope(env)
			if err == nil {
				if key != nil && hasSig {
					if err := verifyRS256(headerBytes, payloadBytes, sigBytes, key); err != nil {
						return IncomingPaymentNotification{}, err
					}
				}
				return buildNotificationFromPayload(payloadBytes, map[string]any{
					"format":       "json-envelope",
					"headerRaw":    jsonRawOrNil(headerBytes),
					"signatureB64": env.Signature,
				})
			}
			// если envelope не сложился — упадём в "голый" payload ниже
		}

		// Формат 3: "голый" payload
		return buildNotificationFromPayload(body, map[string]any{
			"format": "plain-payload",
		})
	}

	return IncomingPaymentNotification{}, fmt.Errorf("unknown webhook format")
}

// ===== Внутренние помощники

func isCompactJWT(b []byte) bool {
	// очень быстрый хак: три точки
	// и отсутствующие пробелы/фигурные скобки в начале
	s := string(b)
	return strings.Count(s, ".") == 2 && !strings.ContainsAny(s, "{ }")
}

func splitCompactJWT(s string) (h, p, sig string) {
	parts := strings.SplitN(s, ".", 3)
	return parts[0], parts[1], parts[2]
}

func decodeEnvelope(env jwtEnvelope) (payloadBytes, headerBytes, sigBytes []byte, hasSig bool, err error) {
	headerBytes, err = decodeJWTSection(env.Header)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("decode envelope header: %w", err)
	}
	payloadBytes, err = decodeJWTSection(env.Payload)
	if err != nil {
		return nil, nil, nil, false, fmt.Errorf("decode envelope payload: %w", err)
	}
	if strings.TrimSpace(env.Signature) != "" {
		sigBytes, err = b64urlOrStdDecode(env.Signature)
		if err != nil {
			return nil, nil, nil, false, fmt.Errorf("decode envelope signature: %w", err)
		}
		hasSig = true
	}
	return payloadBytes, headerBytes, sigBytes, hasSig, nil
}

func decodeJWTSection(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty section")
	}
	// Если это строка — там base64; если объект — возвращаем как есть
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("parse string: %w", err)
		}
		return b64urlOrStdDecode(encoded)
	}
	return trimmed, nil
}

func b64urlOrStdDecode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty b64")
	}
	if dec, err := base64.RawURLEncoding.DecodeString(s); err == nil {
		return dec, nil
	}
	// пробуем стандартную (с паддингом), заменив URL-символы
	std := strings.ReplaceAll(strings.ReplaceAll(s, "-", "+"), "_", "/")
	if dec, err := base64.StdEncoding.DecodeString(std); err == nil {
		return dec, nil
	}
	return nil, fmt.Errorf("invalid base64")
}

func verifyRS256(headerBytes, payloadBytes, sig []byte, key *rsa.PublicKey) error {
	// Проверяем alg
	var hdr jwtHeader
	if err := json.Unmarshal(headerBytes, &hdr); err != nil {
		return fmt.Errorf("parse jwt header: %w", err)
	}
	if !strings.EqualFold(hdr.Alg, "rs256") {
		return ErrUnsupportedJWTAlg
	}

	signed := strings.Join([]string{
		base64.RawURLEncoding.EncodeToString(headerBytes),
		base64.RawURLEncoding.EncodeToString(payloadBytes),
	}, ".")
	sum := sha256.Sum256([]byte(signed))

	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return errors.Join(ErrInvalidWebhookSignature, err)
	}
	return nil
}

func buildNotificationFromPayload(payloadBytes []byte, rawExtra map[string]any) (IncomingPaymentNotification, error) {
	// Держим "сырой" JSON
	var raw map[string]any
	if err := json.Unmarshal(payloadBytes, &raw); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("parse payload json: %w", err)
	}

	// Читаем известные поля
	var p sbpPayload
	if err := json.Unmarshal(payloadBytes, &p); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("bind payload fields: %w", err)
	}

	// Пытаемся распарсить сумму (опционально)
	var amt Amount
	if p.AmountStr != "" {
		if a, err := AmountFromString(p.AmountStr); err == nil {
			amt = a
		}
	}

	// Дата платежа (если вдруг приходит строкой)
	var payDate *time.Time
	if p.PaymentDateStr != "" {
		if t, err := tryParseTime(p.PaymentDateStr); err == nil {
			payDate = &t
		}
	}

	// Собираем уведомление
	ntf := IncomingPaymentNotification{
		Event:          firstNonEmpty(p.WebhookType, rawString(raw, "webhookType")),
		ID:             firstNonEmpty(p.OperationID, rawString(raw, "operationId")),
		OrderID:        firstNonEmpty(p.OrderID, rawString(raw, "orderId")),
		PaymentID:      firstNonEmpty(p.PaymentID, firstNonEmpty(p.RefTransactionID, rawString(raw, "paymentId"))),
		QRID:           firstNonEmpty(p.QRID, rawString(raw, "qrcId")),
		Status:         firstNonEmpty(p.Status, rawString(raw, "status")),
		PaymentPurpose: firstNonEmpty(p.Purpose, rawString(raw, "purpose")),
		PaymentDate:    payDate,
		Amount:         amt,
		PayerName:      firstNonEmpty(p.PayerName, rawString(raw, "payerName")),
		PayerINN:       firstNonEmpty(p.PayerINN, rawString(raw, "payerInn")),
		PayerAccount:   firstNonEmpty(p.PayerAccount, rawString(raw, "payerAccount")),
		PayerBankName:  firstNonEmpty(p.PayerBankName, rawString(raw, "payerBankName")),
		Payload:        raw, // весь payload как есть
		Raw:            map[string]any{"payload": raw},
	}

	// добавим extra-инфо в Raw (не переписывая существующие ключи)
	for k, v := range rawExtra {
		if _, ok := ntf.Raw[k]; !ok {
			ntf.Raw[k] = v
		}
	}
	return ntf, nil
}

func tryParseTime(s string) (time.Time, error) {
	// Попробуем пару частых форматов; при желании расширьте
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"02.01.2006 15:04:05",
		"02.01.2006",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %q", s)
}

func rawString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func jsonRawOrNil(b []byte) any {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(b, &v) == nil {
		return v
	}
	return string(b) // в крайнем случае вернем как строку
}
