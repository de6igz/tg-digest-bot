package tochka

import (
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
)

// минимальный JWK для RSA-публичного ключа
type rsaJWK struct {
	Kty string `json:"kty"` // "RSA" или "rsa"
	N   string `json:"n"`   // modulus (base64url)
	E   string `json:"e"`   // exponent (base64url)
}

// ParseRSAPublicKeyFromJWK парсит публичный RSA-ключ из JWK (JSON с полями kty, n, e).
// Ожидается base64url без паддинга для n и e.
func ParseRSAPublicKeyFromJWK(data []byte) (*rsa.PublicKey, error) {
	var jwk rsaJWK
	if err := json.Unmarshal(data, &jwk); err != nil {
		return nil, fmt.Errorf("parse jwk: %w", err)
	}
	if !strings.EqualFold(jwk.Kty, "rsa") {
		return nil, fmt.Errorf("unsupported jwk kty: %s", jwk.Kty)
	}

	modulusBytes, err := b64urlOrStdDecode(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("decode jwk modulus: %w", err)
	}
	exponentBytes, err := b64urlOrStdDecode(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("decode jwk exponent: %w", err)
	}

	// big-endian bytes -> int
	e := 0
	for _, b := range exponentBytes {
		e = (e << 8) | int(b)
	}
	if e <= 0 {
		return nil, fmt.Errorf("invalid jwk exponent")
	}

	n := new(big.Int).SetBytes(modulusBytes)
	if n.Sign() <= 0 {
		return nil, fmt.Errorf("invalid jwk modulus")
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}
