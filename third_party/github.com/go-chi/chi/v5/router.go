package chi

import "net/http"

type Router struct {
mux *http.ServeMux
}

func NewRouter() *Router {
return &Router{mux: http.NewServeMux()}
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
r.mux.ServeHTTP(w, req)
}

func (r *Router) Use(middlewares ...func(http.Handler) http.Handler) {}

func (r *Router) method(method, pattern string, handler http.HandlerFunc) {
r.mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
if req.Method != method {
http.NotFound(w, req)
return
}
handler(w, req)
})
}

func (r *Router) Get(pattern string, handler http.HandlerFunc)    { r.method(http.MethodGet, pattern, handler) }
func (r *Router) Post(pattern string, handler http.HandlerFunc)   { r.method(http.MethodPost, pattern, handler) }
func (r *Router) Delete(pattern string, handler http.HandlerFunc) { r.method(http.MethodDelete, pattern, handler) }
func (r *Router) Put(pattern string, handler http.HandlerFunc)    { r.method(http.MethodPut, pattern, handler) }
