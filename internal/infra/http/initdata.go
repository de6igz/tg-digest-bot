package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5/middleware"
)

// WebAppAuthMiddleware проверяет initData по токену бота.
func WebAppAuthMiddleware(botToken string) func(http.Handler) http.Handler {
	secret := sha256.Sum256([]byte(botToken))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			initData := r.URL.Query().Get("init_data")
			if initData == "" {
				http.Error(w, "init_data отсутствует", http.StatusUnauthorized)
				return
			}
			if !validateInitData(initData, secret[:]) {
				http.Error(w, "подпись недействительна", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func validateInitData(initData string, secret []byte) bool {
	parts := strings.Split(initData, "&")
	sort.Strings(parts)
	h := hmac.New(sha256.New, secret)
	data := strings.Join(parts[:len(parts)-1], "\n")
	h.Write([]byte(data))
	calc := h.Sum(nil)
	sigKV := strings.Split(parts[len(parts)-1], "=")
	if len(sigKV) != 2 || sigKV[0] != "hash" {
		return false
	}
	expected, err := hex.DecodeString(sigKV[1])
	if err != nil {
		return false
	}
	return hmac.Equal(calc, expected)
}

// RequestID возвращает request ID из контекста chi.
func RequestID(r *http.Request) string {
	return middleware.GetReqID(r.Context())
}

// ErrorResponse описывает ошибку.
type ErrorResponse struct {
	Error string `json:"error"`
}

// WriteError отправляет JSON с ошибкой.
func WriteError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"%s"}`, err.Error())))
}
