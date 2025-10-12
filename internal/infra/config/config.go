package config

import (
	"log"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// AppConfig описывает конфигурацию сервисов.
type AppConfig struct {
	AppEnv string `envconfig:"APP_ENV" default:"dev"`
	TZ     string `envconfig:"TZ" default:"Europe/Amsterdam"`
	Port   int    `envconfig:"PORT" default:"8080"`

	Telegram struct {
		Token      string `envconfig:"TG_BOT_TOKEN"`
		WebhookURL string `envconfig:"TG_WEBHOOK_URL"`
		APIID      int    `envconfig:"TG_API_ID"`
		APIHash    string `envconfig:"TG_API_HASH"`
	} `envconfig:""`

	MTProto struct {
		SessionName string `envconfig:"MTPROTO_SESSION_NAME" default:"default"`
		GlobalRPS   int    `envconfig:"MTPROTO_GLOBAL_RPS" default:"20"`
	} `envconfig:""`

	PGDSN string `envconfig:"PG_DSN"`

	RabbitURL string `envconfig:"RABBITMQ_URL"`

	Limits struct {
		FreeChannels int `envconfig:"FREE_CHANNELS_LIMIT" default:"5"`
		DigestMax    int `envconfig:"DIGEST_MAX_ITEMS" default:"10"`
	} `envconfig:""`

	Queues struct {
		Digest string `envconfig:"DIGEST_QUEUE_KEY" default:"digest_jobs"`
	} `envconfig:""`

	OpenAI struct {
		APIKey  string        `envconfig:"OPENAI_API_KEY"`
		BaseURL string        `envconfig:"OPENAI_BASE_URL"`
		Model   string        `envconfig:"OPENAI_MODEL" default:"qwen3:4b"`
		Timeout time.Duration `envconfig:"OPENAI_TIMEOUT" default:"1200s"`
	} `envconfig:""`
}

// Load загружает конфиг из окружения.
func Load() AppConfig {
	var cfg AppConfig
	if err := envconfig.Process("", &cfg); err != nil {
		log.Fatalf("не удалось загрузить конфиг: %v", err)
	}
	return cfg
}
