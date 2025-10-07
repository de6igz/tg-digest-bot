package main

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/mtproto"
	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
)

func main() {
	cfg := config.Load()
	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("collector: нет подключения к БД")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)
	collector, err := mtproto.NewCollector(cfg.Telegram.APIID, cfg.Telegram.APIHash, &mtproto.SessionInMemory{}, log.Logger)
	if err != nil {
		log.Fatal().Err(err).Msg("collector: не удалось создать клиента")
	}

	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			log.Info().Msg("collector tick")
		case <-context.Background().Done():
			return
		}
	}

	_ = repoAdapter
	_ = collector
}
