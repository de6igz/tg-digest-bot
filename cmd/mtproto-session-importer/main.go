package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"tg-digest-bot/internal/adapters/mtproto"
	"tg-digest-bot/internal/adapters/repo"
	"tg-digest-bot/internal/domain"
	"tg-digest-bot/internal/infra/config"
	"tg-digest-bot/internal/infra/db"
)

type telethonAccount struct {
	AppID       int    `json:"app_id"`
	AppHash     string `json:"app_hash"`
	Phone       string `json:"phone"`
	Username    string `json:"username"`
	SessionFile string `json:"session_file"`
	ExtraParams string `json:"extra_params"`
}

func main() {
	var (
		filePath    string
		metaPath    string
		sessionName string
		poolName    string
		apiID       int
		apiHash     string
	)
	flag.StringVar(&filePath, "file", "", "Path to MTProto session file (.session, JSON or string)")
	flag.StringVar(&metaPath, "meta", "", "Path to Telethon account JSON file (optional)")
	flag.StringVar(&sessionName, "name", "", "Name of the MTProto account (defaults to phone/session file name)")
	flag.StringVar(&poolName, "pool", "default", "Pool name for grouping MTProto accounts")
	flag.IntVar(&apiID, "api-id", 0, "MTProto api_id (optional, overrides metadata)")
	flag.StringVar(&apiHash, "api-hash", "", "MTProto api_hash (optional, overrides metadata)")
	flag.Parse()

	var (
		rawMeta  []byte
		meta     telethonAccount
		haveMeta bool
	)

	if metaPath != "" {
		var err error
		rawMeta, err = os.ReadFile(metaPath)
		if err != nil {
			log.Fatal().Err(err).Msg("mtproto-importer: failed to read metadata file")
		}
		if err := json.Unmarshal(rawMeta, &meta); err != nil {
			log.Fatal().Err(err).Msg("mtproto-importer: failed to parse metadata JSON")
		}
		haveMeta = true
		if meta.AppID != 0 {
			apiID = meta.AppID
		}
		if meta.AppHash != "" {
			apiHash = meta.AppHash
		}
		if sessionName == "" {
			if meta.SessionFile != "" {
				sessionName = meta.SessionFile
			} else if meta.Phone != "" {
				sessionName = meta.Phone
			}
		}
	}

	sessionName = strings.TrimSpace(sessionName)
	poolName = strings.TrimSpace(poolName)
	if poolName == "" {
		poolName = "default"
	}
	if sessionName == "" && filePath != "" {
		base := filepath.Base(filePath)
		sessionName = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if sessionName == "" {
		log.Fatal().Msg("mtproto-importer: account name is required (-name or metadata file)")
	}
	if apiID == 0 {
		log.Fatal().Msg("mtproto-importer: api_id is required (metadata file or -api-id)")
	}
	if apiHash == "" {
		log.Fatal().Msg("mtproto-importer: api_hash is required (metadata file or -api-hash)")
	}

	var (
		sessionData []byte
		converted   bool
		err         error
	)

	if filePath != "" {
		sessionData, err = os.ReadFile(filePath)
		if err != nil {
			log.Fatal().Err(err).Msg("mtproto-importer: failed to read session file")
		}
		sessionData, converted, err = mtproto.NormalizeSessionBytes(sessionData)
		if err != nil {
			log.Fatal().Err(err).Msg("mtproto-importer: unsupported MTProto session format")
		}
	} else if haveMeta && meta.ExtraParams != "" {
		sessionData, converted, err = mtproto.NormalizeSessionBytes([]byte(meta.ExtraParams))
		if err != nil {
			log.Fatal().Err(err).Msg("mtproto-importer: failed to convert extra_params session string")
		}
	} else {
		log.Fatal().Msg("mtproto-importer: provide either -file or metadata.extra_params")
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

	accountRecord := domain.MTProtoAccount{
		Name:     sessionName,
		Pool:     poolName,
		APIID:    apiID,
		APIHash:  apiHash,
		Phone:    meta.Phone,
		Username: meta.Username,
		RawJSON:  rawMeta,
	}
	if err := repoAdapter.UpsertMTProtoAccount(ctx, accountRecord); err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to store account metadata")
	}

	if err := repoAdapter.StoreMTProtoSession(ctx, sessionName, sessionData); err != nil {
		var pathErr *os.PathError
		switch {
		case errors.As(err, &pathErr):
			log.Fatal().Err(pathErr).Msg("mtproto-importer: filesystem error while storing session")
		default:
			log.Fatal().Err(err).Msg("mtproto-importer: failed to store session in database")
		}
	}

	if converted {
		fmt.Println("Session payload was converted to gotd JSON format before storing")
	}
	fmt.Printf("Stored MTProto account %q in pool %q (%d bytes session)\n", sessionName, poolName, len(sessionData))
}
