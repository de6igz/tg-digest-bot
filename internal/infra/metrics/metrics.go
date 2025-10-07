package metrics

import "github.com/prometheus/client_golang/prometheus"

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
)

// MustRegister регистрирует метрики.
func MustRegister(registry *prometheus.Registry) {
	registry.MustRegister(CollectorRPS, CollectorErrors, DigestBuildSeconds, BotSendErrors)
}
