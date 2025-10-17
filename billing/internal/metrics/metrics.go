package metrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

var (
	registerOnce sync.Once

	httpRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "billing_http_requests_total",
		Help: "Общее количество HTTP-запросов в сервис биллинга.",
	}, []string{"component", "method", "path", "status"})

	httpRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "billing_http_request_duration_seconds",
		Help:    "Длительность HTTP-запросов в сервис биллинга.",
		Buckets: prometheus.DefBuckets,
	}, []string{"component", "method", "path", "status"})

	httpRequestsInFlight = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "billing_http_requests_in_flight",
		Help: "Количество текущих HTTP-запросов в обработке.",
	}, []string{"component"})
)

// MustRegister регистрирует метрики пакета в переданном реестре.
func MustRegister(registerer prometheus.Registerer) {
	registerOnce.Do(func() {
		registerer.MustRegister(
			httpRequestsTotal,
			httpRequestDuration,
			httpRequestsInFlight,
		)
	})
}

// StartServer запускает HTTP-сервер, публикующий метрики Prometheus.
func StartServer(ctx context.Context, logger zerolog.Logger, addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())

	go func() {
		select {
		case <-ctx.Done():
		case <-shutdownCtx.Done():
		}
		shutdownTimeout, timeoutCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer timeoutCancel()
		if err := srv.Shutdown(shutdownTimeout); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("billing metrics: graceful shutdown failed")
		}
	}()

	go func() {
		logger.Info().Str("addr", addr).Msg("billing metrics: server started")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("billing metrics: server stopped")
		}
		cancel()
	}()
}

// EchoMiddleware возвращает middleware для сбора метрик HTTP-запросов Echo.
func EchoMiddleware(component string) echo.MiddlewareFunc {
	if component == "" {
		component = "default"
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()
			httpRequestsInFlight.WithLabelValues(component).Inc()
			defer httpRequestsInFlight.WithLabelValues(component).Dec()

			err := next(c)

			statusCode := c.Response().Status
			if statusCode == 0 {
				statusCode = http.StatusOK
			}

			path := c.Path()
			if path == "" {
				path = c.Request().URL.Path
			}

			labels := []string{
				component,
				c.Request().Method,
				path,
				strconv.Itoa(statusCode),
			}

			duration := time.Since(start).Seconds()
			httpRequestDuration.WithLabelValues(labels...).Observe(duration)
			httpRequestsTotal.WithLabelValues(labels...).Inc()

			return err
		}
	}
}
