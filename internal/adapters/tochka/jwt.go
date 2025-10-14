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
	"math/big"
	"strings"
)

var ErrInvalidWebhookSignature = errors.New("tochka: invalid webhook signature")

type rsaJWK struct {
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

func ParseRSAPublicKeyFromJWK(data []byte) (*rsa.PublicKey, error) {
	var jwk rsaJWK
	if err := json.Unmarshal(data, &jwk); err != nil {
		return nil, fmt.Errorf("parse jwk: %w", err)
	}
	if !strings.EqualFold(jwk.Kty, "rsa") {
		return nil, fmt.Errorf("unsupported jwk kty: %s", jwk.Kty)
	}
	modulusBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(jwk.N))
	if err != nil {
		return nil, fmt.Errorf("decode jwk modulus: %w", err)
	}
	exponentBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(jwk.E))
	if err != nil {
		return nil, fmt.Errorf("decode jwk exponent: %w", err)
	}
	exponent := 0
	for _, b := range exponentBytes {
		exponent = (exponent << 8) | int(b)
	}
	if exponent == 0 {
		return nil, fmt.Errorf("invalid jwk exponent")
	}
	modulus := new(big.Int).SetBytes(modulusBytes)
	if modulus.Sign() <= 0 {
		return nil, fmt.Errorf("invalid jwk modulus")
	}
	return &rsa.PublicKey{N: modulus, E: exponent}, nil
}

func ParseIncomingPaymentNotificationJWT(token []byte, key *rsa.PublicKey) (IncomingPaymentNotification, error) {
	if key == nil {
		return IncomingPaymentNotification{}, fmt.Errorf("webhook public key is nil")
	}
	trimmed := bytes.TrimSpace(token)
	if len(trimmed) == 0 {
		return IncomingPaymentNotification{}, fmt.Errorf("empty webhook payload")
	}
	if trimmed[0] == '{' {
		return parseIncomingPaymentNotificationJWTJSON(trimmed, key)
	}
	return parseIncomingPaymentNotificationJWTCompact(string(trimmed), key)
}

func parseIncomingPaymentNotificationJWTCompact(compact string, key *rsa.PublicKey) (IncomingPaymentNotification, error) {
	parts := strings.Split(compact, ".")
	if len(parts) != 3 {
		return IncomingPaymentNotification{}, fmt.Errorf("invalid jwt format")
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("parse jwt header: %w", err)
	}
	if !strings.EqualFold(header.Alg, "rs256") {
		return IncomingPaymentNotification{}, fmt.Errorf("unsupported jwt alg: %s", header.Alg)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt payload: %w", err)
	}
	signature, err := decodeJWTBase64String(parts[2])
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt signature: %w", err)
	}
	signed := strings.Join(parts[:2], ".")
	sum := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature); err != nil {
		return IncomingPaymentNotification{}, errors.Join(ErrInvalidWebhookSignature, err)
	}
	return ParseIncomingPaymentNotification(payloadBytes)
}

func parseIncomingPaymentNotificationJWTJSON(data []byte, key *rsa.PublicKey) (IncomingPaymentNotification, error) {
	var envelope struct {
		Header    json.RawMessage `json:"header"`
		Payload   json.RawMessage `json:"payload"`
		Signature string          `json:"signature"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("parse jwt envelope: %w", err)
	}
	headerBytes, err := decodeJWTSection(envelope.Header)
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt header: %w", err)
	}
	payloadBytes, err := decodeJWTSection(envelope.Payload)
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt payload: %w", err)
	}
	signature, err := decodeJWTBase64String(envelope.Signature)
	if err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("decode jwt signature: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return IncomingPaymentNotification{}, fmt.Errorf("parse jwt header: %w", err)
	}
	if !strings.EqualFold(header.Alg, "rs256") {
		return IncomingPaymentNotification{}, fmt.Errorf("unsupported jwt alg: %s", header.Alg)
	}
	signed := strings.Join([]string{
		base64.RawURLEncoding.EncodeToString(headerBytes),
		base64.RawURLEncoding.EncodeToString(payloadBytes),
	}, ".")
	sum := sha256.Sum256([]byte(signed))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature); err != nil {
		return IncomingPaymentNotification{}, errors.Join(ErrInvalidWebhookSignature, err)
	}
	return ParseIncomingPaymentNotification(payloadBytes)
}

func decodeJWTSection(raw json.RawMessage) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty section")
	}
	if trimmed[0] == '"' {
		var encoded string
		if err := json.Unmarshal(trimmed, &encoded); err != nil {
			return nil, fmt.Errorf("parse string: %w", err)
		}
		decoded, err := decodeJWTBase64String(encoded)
		if err != nil {
			return nil, err
		}
		return decoded, nil
	}
	return trimmed, nil
}

func decodeJWTBase64String(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty value")
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	// try standard encoding with padding, also replacing URL-safe characters if necessary
	standard := strings.ReplaceAll(strings.ReplaceAll(value, "-", "+"), "_", "/")
	if decoded, err := base64.StdEncoding.DecodeString(standard); err == nil {
		return decoded, nil
	}
	return nil, fmt.Errorf("invalid base64")
}
