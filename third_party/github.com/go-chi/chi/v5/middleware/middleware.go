package middleware

import (
"context"
"net/http"
"time"
)

func RequestID(next http.Handler) http.Handler { return next }
func RealIP(next http.Handler) http.Handler   { return next }
func Logger(next http.Handler) http.Handler   { return next }
func Recoverer(next http.Handler) http.Handler { return next }

func Timeout(d time.Duration) func(http.Handler) http.Handler {
return func(next http.Handler) http.Handler {
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
ctx, cancel := context.WithTimeout(r.Context(), d)
defer cancel()
next.ServeHTTP(w, r.WithContext(ctx))
})
}
}

func GetReqID(ctx context.Context) string {
return ""
}
