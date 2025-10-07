package promhttp

import "net/http"

func Handler() http.Handler {
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
w.WriteHeader(http.StatusOK)
})
}
