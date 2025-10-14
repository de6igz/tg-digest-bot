package config

import (
    "log"
    "time"

    "github.com/kelseyhightower/envconfig"
)

type Config struct {
    Port int    `envconfig:"PORT" default:"8082"`
    PGDSN string `envconfig:"PG_DSN"`

    Server struct {
        ShutdownTimeout time.Duration `envconfig:"SERVER_SHUTDOWN_TIMEOUT" default:"5s"`
        ReadTimeout     time.Duration `envconfig:"SERVER_READ_TIMEOUT" default:"15s"`
        WriteTimeout    time.Duration `envconfig:"SERVER_WRITE_TIMEOUT" default:"15s"`
        IdleTimeout     time.Duration `envconfig:"SERVER_IDLE_TIMEOUT" default:"60s"`
    } `envconfig:""`

    Metrics struct {
        Enabled bool   `envconfig:"METRICS_ENABLED" default:"false"`
        Addr    string `envconfig:"METRICS_ADDR" default:":9091"`
    } `envconfig:""`
}

func Load() Config {
    var cfg Config
    if err := envconfig.Process("", &cfg); err != nil {
        log.Fatalf("billing: failed to load config: %v", err)
    }
    return cfg
}
