package metrics

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
)

var (
	CollectorRPS = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "collector_rps",
		Help: "Текущий запросов в секунду при сборе",
	})
	CollectorErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "collector_errors_total",
		Help: "Ошибки при сборе каналов",
	})
	DigestBuildSeconds = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "digest_build_seconds",
		Help:    "Время построения дайджеста",
		Buckets: prometheus.DefBuckets,
	})
	BotSendErrors = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "bot_send_errors_total",
		Help: "Ошибки отправки сообщений ботом",
	})

	NetworkRequestDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "network_request_duration_seconds",
		Help:    "Длительность сетевых запросов",
		Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55, 60, 65, 70, 75, 80, 85, 90, 95, 100, 105, 110, 115, 120, 125, 130, 135, 140, 145, 150, 155, 160, 165, 170, 175, 180, 185, 190, 195, 200, 250, 300, 350, 400, 450, 500, 550, 600},
	}, []string{"component", "operation", "target", "status"})

	NetworkRequestTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "network_request_total",
		Help: "Количество сетевых запросов",
	}, []string{"component", "operation", "target", "status"})

	LLMGenerationDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "llm_generation_duration_seconds",
		Help:    "Длительность генерации ответа LLM",
		Buckets: prometheus.DefBuckets,
	}, []string{"model"})

	LLMTokensTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "llm_tokens_total",
		Help: "Количество токенов, использованных LLM",
	}, []string{"model", "type"})

	DigestRequestsTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "digest_requests_total",
		Help: "Общее количество запросов на построение дайджеста",
	})

	DigestRequestsByUser = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "digest_requests_by_user_total",
		Help: "Количество запросов на построение дайджеста по пользователям",
	}, []string{"user_id"})

	DigestRequestsByChannel = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "digest_requests_by_channel_total",
		Help: "Количество запросов на построение дайджеста по каналам",
	}, []string{"channel_id"})
)

// MustRegister регистрирует метрики.
func MustRegister(registerer prometheus.Registerer) {
	registerer.MustRegister(
		CollectorRPS,
		CollectorErrors,
		DigestBuildSeconds,
		BotSendErrors,
		NetworkRequestDuration,
		NetworkRequestTotal,
		LLMGenerationDuration,
		LLMTokensTotal,
		DigestRequestsTotal,
		DigestRequestsByUser,
		DigestRequestsByChannel,
	)
}

// StartServer запускает HTTP сервер с эндпоинтом /metrics.
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
			logger.Error().Err(err).Msg("metrics: graceful shutdown failed")
		}
	}()

	go func() {
		logger.Info().Str("addr", addr).Msg("metrics: server started")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("metrics: server stopped")
		}
		cancel()
	}()
}

// ObserveNetworkRequest записывает длительность и статус сетевого запроса.
func ObserveNetworkRequest(component, operation, target string, start time.Time, err error) {
	if component == "" {
		component = "unknown"
	}
	if operation == "" {
		operation = "unknown"
	}
	if target == "" {
		target = "unknown"
	}
	status := "success"
	if err != nil {
		status = "error"
	}
	duration := time.Since(start).Seconds()
	NetworkRequestDuration.WithLabelValues(component, operation, target, status).Observe(duration)
	NetworkRequestTotal.WithLabelValues(component, operation, target, status).Inc()
}

// ObserveLLMGeneration записывает длительность и токены генерации LLM.
func ObserveLLMGeneration(model string, duration time.Duration, promptTokens, completionTokens, totalTokens int) {
	if model == "" {
		model = "unknown"
	}
	LLMGenerationDuration.WithLabelValues(model).Observe(duration.Seconds())
	if promptTokens > 0 {
		LLMTokensTotal.WithLabelValues(model, "prompt").Add(float64(promptTokens))
	}
	if completionTokens > 0 {
		LLMTokensTotal.WithLabelValues(model, "completion").Add(float64(completionTokens))
	}
	if totalTokens <= 0 {
		totalTokens = promptTokens + completionTokens
	}
	if totalTokens > 0 {
		LLMTokensTotal.WithLabelValues(model, "total").Add(float64(totalTokens))
	}
}

// IncDigestOverall увеличивает общий счётчик запросов на дайджест.
func IncDigestOverall() {
	DigestRequestsTotal.Inc()
}

// IncDigestForUser увеличивает счётчик запросов на дайджест для пользователя.
func IncDigestForUser(userID int64) {
	DigestRequestsByUser.WithLabelValues(strconv.FormatInt(userID, 10)).Inc()
}

// IncDigestForChannel увеличивает счётчик запросов на дайджест для канала.
func IncDigestForChannel(channelID int64) {
	DigestRequestsByChannel.WithLabelValues(strconv.FormatInt(channelID, 10)).Inc()
}
