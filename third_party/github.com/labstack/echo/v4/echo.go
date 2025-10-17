package echo

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

type HandlerFunc func(Context) error

type MiddlewareFunc func(HandlerFunc) HandlerFunc

type HTTPErrorHandler func(error, Context)

type Context interface {
	Request() *http.Request
	Response() *Response
	Param(string) string
	QueryParam(string) string
	Bind(any) error
	JSON(int, any) error
	Blob(int, string, []byte) error
	Echo() *Echo
	Path() string
}

type Response struct {
	Writer    http.ResponseWriter
	Status    int
	Committed bool
}

func (r *Response) WriteHeader(code int) {
	if r.Committed {
		return
	}
	r.Status = code
	r.Writer.WriteHeader(code)
	r.Committed = true
}

type Echo struct {
	routes           []*route
	middleware       []MiddlewareFunc
	HTTPErrorHandler HTTPErrorHandler
	HideBanner       bool
	HidePort         bool
}

type route struct {
	method   string
	segments []string
	handler  HandlerFunc
}

func New() *Echo {
	return &Echo{
		HTTPErrorHandler: defaultHTTPErrorHandler,
	}
}

func (e *Echo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, route := range e.routes {
		if route.method != r.Method {
			continue
		}
		params, ok := route.match(r.URL.Path)
		if !ok {
			continue
		}
		ctx := newContext(e, w, r, params)
		handler := route.handler
		for i := len(e.middleware) - 1; i >= 0; i-- {
			handler = e.middleware[i](handler)
		}
		if err := handler(ctx); err != nil {
			e.HTTPErrorHandler(err, ctx)
		}
		return
	}
	http.NotFound(w, r)
}

func (e *Echo) Use(m ...MiddlewareFunc) {
	e.middleware = append(e.middleware, m...)
}

func (e *Echo) GET(path string, h HandlerFunc) {
	e.addRoute(http.MethodGet, path, h)
}

func (e *Echo) POST(path string, h HandlerFunc) {
	e.addRoute(http.MethodPost, path, h)
}

func (e *Echo) Any(path string, h HandlerFunc) {
	methods := []string{
		http.MethodGet,
		http.MethodHead,
		http.MethodPost,
		http.MethodPut,
		http.MethodPatch,
		http.MethodDelete,
		http.MethodConnect,
		http.MethodOptions,
		http.MethodTrace,
	}
	for _, method := range methods {
		e.addRoute(method, path, h)
	}
}

func (e *Echo) addRoute(method, path string, h HandlerFunc) {
	segments := splitPath(path)
	e.routes = append(e.routes, &route{method: method, segments: segments, handler: h})
}

func (r *route) match(path string) (map[string]string, bool) {
	pathSegments := splitPath(path)
	if len(pathSegments) != len(r.segments) {
		return nil, false
	}
	params := make(map[string]string, len(r.segments))
	for i, segment := range r.segments {
		if strings.HasPrefix(segment, ":") {
			params[segment[1:]] = pathSegments[i]
			continue
		}
		if segment != pathSegments[i] {
			return nil, false
		}
	}
	return params, true
}

func splitPath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return []string{}
	}
	return strings.Split(trimmed, "/")
}

func defaultHTTPErrorHandler(err error, c Context) {
	if err == nil {
		return
	}
	if !c.Response().Committed {
		c.Response().Writer.Header().Set("Content-Type", "application/json")
		c.Response().WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(c.Response().Writer).Encode(map[string]string{
			"error": err.Error(),
		})
	}
}

type context struct {
	e        *Echo
	request  *http.Request
	response *Response
	params   map[string]string
}

func newContext(e *Echo, w http.ResponseWriter, r *http.Request, params map[string]string) *context {
	return &context{
		e:        e,
		request:  r,
		response: &Response{Writer: w},
		params:   params,
	}
}

func (c *context) Request() *http.Request {
	return c.request
}

func (c *context) Response() *Response {
	return c.response
}

func (c *context) Param(name string) string {
	return c.params[name]
}

func (c *context) QueryParam(name string) string {
	return c.request.URL.Query().Get(name)
}

func (c *context) Bind(v any) error {
	if v == nil {
		return errors.New("echo: nil bind target")
	}
	decoder := json.NewDecoder(c.request.Body)
	return decoder.Decode(v)
}

func (c *context) JSON(code int, v any) error {
	if !c.response.Committed {
		c.response.Writer.Header().Set("Content-Type", "application/json")
	}
	c.response.WriteHeader(code)
	return json.NewEncoder(c.response.Writer).Encode(v)
}

func (c *context) Blob(code int, contentType string, b []byte) error {
	if !c.response.Committed {
		c.response.Writer.Header().Set("Content-Type", contentType)
	}
	c.response.WriteHeader(code)
	if len(b) == 0 {
		return nil
	}
	_, err := c.response.Writer.Write(b)
	return err
}

func (c *context) Echo() *Echo {
	return c.e
}

func (c *context) Path() string {
	if c.request == nil {
		return ""
	}
	return c.request.URL.Path
}
