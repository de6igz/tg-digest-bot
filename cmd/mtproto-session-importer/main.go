package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
)

func main() {
	var (
		filePath    string
		sessionName string
	)
	flag.StringVar(&filePath, "file", "", "Path to MTProto session JSON file")
	flag.StringVar(&sessionName, "name", "default", "Name of the MTProto session")
	flag.Parse()

	if filePath == "" {
		log.Fatal().Msg("mtproto-importer: path to session file is required (-file)")
	}

	sessionData, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to read session file")
	}
	if !json.Valid(sessionData) {
		log.Fatal().Msg("mtproto-importer: session file must contain valid JSON")
	}

	cfg := config.Load()
	if cfg.PGDSN == "" {
		log.Fatal().Msg("mtproto-importer: PG_DSN environment variable is required")
	}

	pool, err := db.Connect(cfg.PGDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to connect to database")
	}
	defer pool.Close()

	repoAdapter := repo.NewPostgres(pool)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := repoAdapter.StoreMTProtoSession(ctx, sessionName, sessionData); err != nil {
		var pathErr *os.PathError
		switch {
		case errors.As(err, &pathErr):
			log.Fatal().Err(pathErr).Msg("mtproto-importer: filesystem error while storing session")
		default:
			log.Fatal().Err(err).Msg("mtproto-importer: failed to store session in database")
		}
	}

	fmt.Printf("Stored MTProto session %q (%d bytes) in database\n", sessionName, len(sessionData))
}
