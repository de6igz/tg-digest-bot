package config

import (
	"log"

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
		SessionFile string `envconfig:"MTPROTO_SESSION_FILE"`
		GlobalRPS   int    `envconfig:"MTPROTO_GLOBAL_RPS" default:"20"`
	} `envconfig:""`

	PGDSN string `envconfig:"PG_DSN"`

	RedisAddr string `envconfig:"REDIS_ADDR"`

	Limits struct {
		FreeChannels int `envconfig:"FREE_CHANNELS_LIMIT" default:"5"`
		DigestMax    int `envconfig:"DIGEST_MAX_ITEMS" default:"10"`
	} `envconfig:""`

	Queues struct {
		Digest string `envconfig:"DIGEST_QUEUE_KEY" default:"digest_jobs"`
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
