package httpapi

import (
	_ "embed"
	"net/http"

	"github.com/labstack/echo/v4"
)

var (
	//go:embed openapi.yaml
	openAPISpec []byte
)

func (s *Server) handleSwagger(c echo.Context) error {
	return c.Blob(http.StatusOK, "application/yaml", openAPISpec)
}
