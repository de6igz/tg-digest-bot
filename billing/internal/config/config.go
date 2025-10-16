package config

import (
	"log"
	"time"

	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	Port        int    `envconfig:"PORT" default:"8082"`
	WebhookPort int    `envconfig:"WEBHOOK_PORT" default:"18082"`
	PGDSN       string `envconfig:"BILLING_PG_DSN"`
	APIToken    string `envconfig:"BILLING_API_TOKEN"`

	Server struct {
		ShutdownTimeout time.Duration `envconfig:"SERVER_SHUTDOWN_TIMEOUT" default:"500s"`
		ReadTimeout     time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"150s"`
		WriteTimeout    time.Duration `envconfig:"SERVER_WRITE_TIMEOUT" default:"150s"`
		IdleTimeout     time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"60s"`
	} `envconfig:""`

	Metrics struct {
		Enabled bool   `envconfig:"METRICS_ENABLED" default:"false"`
		Addr    string `envconfig:"METRICS_ADDR" default:":9091"`
	} `envconfig:""`

	Tochka struct {
		BaseURL         string        `envconfig:"TOCHKA_BASE_URL" default:"https://enter.tochka.com"`
		MerchantID      string        `envconfig:"TOCHKA_MERCHANT_ID"`
		AccountID       string        `envconfig:"TOCHKA_ACCOUNT_ID"`
		AccessToken     string        `envconfig:"TOCHKA_ACCESS_TOKEN"`
		Timeout         time.Duration `envconfig:"TOCHKA_TIMEOUT" default:"15s"`
		NotificationURL string        `envconfig:"TOCHKA_NOTIFICATION_URL"`
		WebhookSecret   string        `envconfig:"TOCHKA_WEBHOOK_SECRET"`
		WebhookKey      string        `envconfig:"TOCHKA_WEBHOOK_PUBLIC_KEY"`
	} `envconfig:""`
}

func Load() Config {
	var cfg Config
	if err := envconfig.Process("", &cfg); err != nil {
		log.Fatalf("billing: failed to load config: %v", err)
	}
	return cfg
}
