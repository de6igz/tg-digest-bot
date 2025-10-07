package chi

import "net/http"

// Router описывает минимальный набор методов, используемых в проекте.
type Router interface {
	http.Handler
	Use(middlewares ...func(http.Handler) http.Handler)
	Method(method, pattern string, handler http.HandlerFunc)
	Get(pattern string, handler http.HandlerFunc)
	Post(pattern string, handler http.HandlerFunc)
	Delete(pattern string, handler http.HandlerFunc)
	Put(pattern string, handler http.HandlerFunc)
}

// Mux — простейшая реализация интерфейса Router.
type Mux struct {
	mux         *http.ServeMux
	middlewares []func(http.Handler) http.Handler
}

// NewRouter возвращает новый Mux.
func NewRouter() *Mux {
	return &Mux{mux: http.NewServeMux()}
}

// ServeHTTP реализует http.Handler.
func (m *Mux) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	m.mux.ServeHTTP(w, req)
}

// Use регистрирует middleware.
func (m *Mux) Use(middlewares ...func(http.Handler) http.Handler) {
	m.middlewares = append(m.middlewares, middlewares...)
}

// Method добавляет хендлер для HTTP метода.
func (m *Mux) Method(method, pattern string, handler http.HandlerFunc) {
	m.handle(pattern, func(w http.ResponseWriter, req *http.Request) {
		if req.Method != method {
			http.NotFound(w, req)
			return
		}
		handler(w, req)
	})
}

// Get добавляет GET хендлер.
func (m *Mux) Get(pattern string, handler http.HandlerFunc) {
	m.Method(http.MethodGet, pattern, handler)
}

// Post добавляет POST хендлер.
func (m *Mux) Post(pattern string, handler http.HandlerFunc) {
	m.Method(http.MethodPost, pattern, handler)
}

// Delete добавляет DELETE хендлер.
func (m *Mux) Delete(pattern string, handler http.HandlerFunc) {
	m.Method(http.MethodDelete, pattern, handler)
}

// Put добавляет PUT хендлер.
func (m *Mux) Put(pattern string, handler http.HandlerFunc) {
	m.Method(http.MethodPut, pattern, handler)
}

func (m *Mux) handle(pattern string, handler http.HandlerFunc) {
	var h http.Handler = http.HandlerFunc(handler)
	for i := len(m.middlewares) - 1; i >= 0; i-- {
		h = m.middlewares[i](h)
	}
	m.mux.Handle(pattern, h)
}
