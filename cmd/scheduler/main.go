package main

import (
"time"

"github.com/rs/zerolog/log"

"tg-digest-bot/internal/adapters/ranker"
"tg-digest-bot/internal/adapters/repo"
"tg-digest-bot/internal/adapters/summarizer"
"tg-digest-bot/internal/infra/config"
"tg-digest-bot/internal/infra/db"
"tg-digest-bot/internal/usecase/digest"
)

func main() {
cfg := config.Load()
pool, err := db.Connect(cfg.PGDSN)
if err != nil {
log.Fatal().Err(err).Msg("scheduler: нет подключения к БД")
}
defer pool.Close()

repoAdapter := repo.NewPostgres(pool)
summarizerAdapter := summarizer.NewSimple()
rankerAdapter := ranker.NewSimple(24)
digestService := digest.NewService(repoAdapter, repoAdapter, repoAdapter, repoAdapter, summarizerAdapter, rankerAdapter, nil, cfg.Limits.DigestMax)

ticker := time.NewTicker(time.Minute)
defer ticker.Stop()
for range ticker.C {
users, err := repoAdapter.ListForDailyTime(time.Now().UTC())
if err != nil {
log.Error().Err(err).Msg("scheduler: ошибка выборки пользователей")
continue
}
for _, user := range users {
if err := digestService.BuildAndSendNow(user.TGUserID); err != nil {
log.Error().Err(err).Int64("user", user.TGUserID).Msg("scheduler: не удалось отправить дайджест")
}
}
}
}
