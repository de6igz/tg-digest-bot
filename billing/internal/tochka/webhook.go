package tochka

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type Amount struct {
	Value    string
	Currency string
}

//type IncomingPaymentNotification struct {
//	Event          string
//	ID             string
//	OrderID        string
//	PaymentID      string
//	QRID           string
//	Status         string
//	PaymentPurpose string
//	PaymentDate    *time.Time
//	Amount         Amount
//	PayerName      string
//	PayerINN       string
//	PayerAccount   string
//	PayerBankName  string
//	Payload        map[string]any
//	Raw            map[string]any
//}

func ParseIncomingPaymentNotification(data []byte) (IncomingPaymentNotification, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw map[string]any
	if err := dec.Decode(&raw); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode webhook: %w", err)
	}
	notif := IncomingPaymentNotification{Raw: raw}
	notif.Event = firstString(raw, "event", "eventType")
	notif.ID = firstString(raw, "id", "eventId", "operationId", "operation_id")

	payload := firstMap(raw, "payload", "data")
	notif.Payload = payload
	if payload != nil {
		notif.OrderID = firstString(payload, "orderId", "order_id")
		if notif.OrderID == "" {
			if orderMap := firstMap(payload, "order"); orderMap != nil {
				notif.OrderID = firstString(orderMap, "orderId", "order_id")
			}
		}
		notif.PaymentID = firstString(payload, "paymentId", "payment_id", "transactionId", "transaction_id", "refTransactionId", "ref_transaction_id")
		notif.QRID = firstString(payload, "qrId", "qrCodeId", "qr_code_id")
		notif.Status = firstString(payload, "status")
		notif.PaymentPurpose = firstString(payload, "paymentPurpose", "purpose")
		notif.Amount = Amount{
			Value:    firstString(payload, "amount"),
			Currency: firstString(payload, "currency"),
		}
		if notif.Amount.Value == "" {
			if amountMap := firstMap(payload, "amount"); amountMap != nil {
				notif.Amount.Value = firstString(amountMap, "value", "amount")
				notif.Amount.Currency = firstString(amountMap, "currency")
			}
		}
		if notif.Amount.Currency == "" {
			notif.Amount.Currency = firstString(payload, "amountCurrency")
		}
		if notif.PaymentPurpose == "" {
			notif.PaymentPurpose = firstString(payload, "description")
		}
		notif.PayerName = firstString(payload, "payerName")
		notif.PayerINN = firstString(payload, "payerInn", "payerINN")
		notif.PayerAccount = firstString(payload, "payerAccount")
		notif.PayerBankName = firstString(payload, "payerBankName")
		if ts := firstString(payload, "paymentDate", "date", "createdAt", "created_at"); ts != "" {
			notif.PaymentDate = parseTime(ts)
		}
	}

	if notif.OrderID == "" {
		notif.OrderID = firstString(raw, "orderId", "order_id")
	}
	if notif.Amount.Value == "" {
		notif.Amount.Value = firstString(raw, "amount")
	}
	if notif.Amount.Currency == "" {
		notif.Amount.Currency = firstString(raw, "currency")
	}

	return notif, nil
}

func (n IncomingPaymentNotification) AmountMinor() (int64, error) {
	if n.Amount.Value == "" {
		return 0, fmt.Errorf("amount value is empty")
	}
	return parseDecimalMinor(n.Amount.Value, 2)
}

func (n IncomingPaymentNotification) IdempotencyKey() string {
	if n.PaymentID != "" {
		return n.PaymentID
	}
	if n.ID != "" {
		return n.ID
	}
	if n.QRID != "" {
		return n.QRID
	}
	return n.OrderID
}

func (n IncomingPaymentNotification) Metadata() map[string]any {
	meta := make(map[string]any, len(n.Raw))
	for k, v := range n.Raw {
		meta[k] = v
	}
	return meta
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if v, ok := m[key]; ok {
			switch value := v.(type) {
			case string:
				if value != "" {
					return value
				}
			case json.Number:
				return value.String()
			case float64:
				return strconv.FormatFloat(value, 'f', -1, 64)
			}
		}
	}
	return ""
}

func firstMap(m map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if key == "" {
			continue
		}
		if v, ok := m[key]; ok {
			if mv, ok := v.(map[string]any); ok {
				return mv
			}
		}
	}
	return nil
}

func parseDecimalMinor(value string, fraction int) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty value")
	}
	negative := false
	if strings.HasPrefix(value, "-") {
		negative = true
		value = strings.TrimPrefix(value, "-")
	}
	parts := strings.SplitN(value, ".", 2)
	wholePart := strings.ReplaceAll(parts[0], " ", "")
	wholePart = strings.ReplaceAll(wholePart, "_", "")
	if wholePart == "" {
		wholePart = "0"
	}
	whole, err := strconv.ParseInt(wholePart, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse whole amount: %w", err)
	}
	var fractionPart string
	if len(parts) == 2 {
		fractionPart = strings.TrimRight(parts[1], "0")
		if len(fractionPart) > fraction {
			fractionPart = fractionPart[:fraction]
		}
	}
	for len(fractionPart) < fraction {
		fractionPart += "0"
	}
	fracValue := int64(0)
	if fractionPart != "" {
		parsed, err := strconv.ParseInt(fractionPart, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse fractional amount: %w", err)
		}
		fracValue = parsed
	}
	result := whole*int64Pow10(fraction) + fracValue
	if negative {
		result = -result
	}
	return result, nil
}

func int64Pow10(power int) int64 {
	result := int64(1)
	for i := 0; i < power; i++ {
		result *= 10
	}
	return result
}
