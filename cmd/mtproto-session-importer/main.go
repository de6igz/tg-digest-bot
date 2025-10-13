package main

import (
	"bytes"
	"context"
	"encoding/json"
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

type sessionBundle struct {
	Name          string          `json:"name"`
	Pool          string          `json:"pool"`
	APIID         int             `json:"api_id"`
	APIHash       string          `json:"api_hash"`
	Phone         string          `json:"phone"`
	Username      string          `json:"username"`
	StringSession string          `json:"string_session"`
	Session       string          `json:"session"`
	SessionJSON   json.RawMessage `json:"session_json"`
	Metadata      json.RawMessage `json:"metadata"`
	RawJSON       json.RawMessage `json:"raw_json"`
}

func (b sessionBundle) metadata() []byte {
	if trimmed := bytes.TrimSpace(b.Metadata); len(trimmed) > 0 {
		return append([]byte(nil), trimmed...)
	}
	if trimmed := bytes.TrimSpace(b.RawJSON); len(trimmed) > 0 {
		return append([]byte(nil), trimmed...)
	}
	return nil
}

func (b sessionBundle) sessionPayload() ([]byte, bool, error) {
	if trimmed := bytes.TrimSpace(b.SessionJSON); len(trimmed) > 0 {
		return append([]byte(nil), trimmed...), false, nil
	}

	sessionString := strings.TrimSpace(b.StringSession)
	if sessionString == "" {
		sessionString = strings.TrimSpace(b.Session)
	}
	if sessionString == "" {
		return nil, false, fmt.Errorf("session bundle is missing string_session or session_json")
	}

	converted, wasConverted, err := mtproto.NormalizeSessionBytes([]byte(sessionString))
	if err != nil {
		return nil, false, err
	}
	return converted, wasConverted, nil
}

func main() {
	var (
		bundlePath   string
		nameOverride string
		poolOverride string
	)

	flag.StringVar(&bundlePath, "bundle", "", "Path to MTProto session bundle JSON (exported by scripts/export_telethon_session.py)")
	flag.StringVar(&bundlePath, "file", "", "Deprecated alias for -bundle")
	flag.StringVar(&nameOverride, "name", "", "Override account name from bundle")
	flag.StringVar(&poolOverride, "pool", "", "Override pool name from bundle")
	flag.Parse()

	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" {
		log.Fatal().Msg("mtproto-importer: -bundle path is required")
	}

	rawBundle, err := os.ReadFile(bundlePath)
	if err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to read bundle file")
	}

	var bundle sessionBundle
	if err := json.Unmarshal(rawBundle, &bundle); err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to parse bundle JSON")
	}

	if name := strings.TrimSpace(nameOverride); name != "" {
		bundle.Name = name
	}
	if pool := strings.TrimSpace(poolOverride); pool != "" {
		bundle.Pool = pool
	}

	if bundle.Name == "" {
		bundle.Name = strings.TrimSuffix(filepath.Base(bundlePath), filepath.Ext(bundlePath))
	}
	bundle.Name = strings.TrimSpace(bundle.Name)
	if bundle.Name == "" {
		log.Fatal().Msg("mtproto-importer: account name is required")
	}

	bundle.Pool = strings.TrimSpace(bundle.Pool)
	if bundle.Pool == "" {
		bundle.Pool = "default"
	}

	if bundle.APIID == 0 {
		log.Fatal().Msg("mtproto-importer: api_id is required in the bundle")
	}
	bundle.APIHash = strings.TrimSpace(bundle.APIHash)
	if bundle.APIHash == "" {
		log.Fatal().Msg("mtproto-importer: api_hash is required in the bundle")
	}

	sessionData, converted, err := bundle.sessionPayload()
	if err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: unsupported MTProto session format")
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
		Name:     bundle.Name,
		Pool:     bundle.Pool,
		APIID:    bundle.APIID,
		APIHash:  bundle.APIHash,
		Phone:    strings.TrimSpace(bundle.Phone),
		Username: strings.TrimSpace(bundle.Username),
		RawJSON:  bundle.metadata(),
	}

	if err := repoAdapter.UpsertMTProtoAccount(ctx, accountRecord); err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to store account metadata")
	}

	if err := repoAdapter.StoreMTProtoSession(ctx, bundle.Name, sessionData); err != nil {
		log.Fatal().Err(err).Msg("mtproto-importer: failed to store session in database")
	}

	if converted {
		fmt.Println("Session payload was converted to gotd JSON format before storing")
	}
	fmt.Printf("Stored MTProto account %q in pool %q (%d bytes session)\n", bundle.Name, bundle.Pool, len(sessionData))
}
