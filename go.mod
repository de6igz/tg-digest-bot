module tg-digest-bot

go 1.24

require (
        github.com/go-chi/chi/v5 v5.0.0
        github.com/go-chi/chi/v5/middleware v0.0.0-00010101000000-000000000000
        github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.0.0
        github.com/gotd/td v0.0.0
        github.com/jackc/pgx/v5 v5.5.4
        github.com/jackc/pgx/v5/pgxpool v0.0.0-00010101000000-000000000000
        github.com/kelseyhightower/envconfig v1.4.0
        github.com/prometheus/client_golang v1.0.0
        github.com/prometheus/client_golang/prometheus/promhttp v0.0.0-00010101000000-000000000000
        github.com/redis/go-redis/v9 v9.0.0
        github.com/rs/zerolog v1.0.0
        github.com/rs/zerolog/log v0.0.0-00010101000000-000000000000
)

replace github.com/go-chi/chi/v5 => ./third_party/github.com/go-chi/chi/v5

replace github.com/go-chi/chi/v5/middleware => ./third_party/github.com/go-chi/chi/v5/middleware

replace github.com/go-telegram-bot-api/telegram-bot-api/v5 => ./third_party/github.com/go-telegram-bot-api/telegram-bot-api/v5

replace github.com/gotd/td => ./third_party/github.com/gotd/td

replace github.com/jackc/pgx/v5 => ./third_party/github.com/jackc/pgx/v5

replace github.com/jackc/pgx/v5/pgxpool => ./third_party/github.com/jackc/pgx/v5/pgxpool

replace github.com/kelseyhightower/envconfig => ./third_party/github.com/kelseyhightower/envconfig

replace github.com/prometheus/client_golang => ./third_party/github.com/prometheus/client_golang

replace github.com/prometheus/client_golang/prometheus/promhttp => ./third_party/github.com/prometheus/client_golang/prometheus/promhttp

replace github.com/redis/go-redis/v9 => ./third_party/github.com/redis/go-redis/v9

replace github.com/rs/zerolog => ./third_party/github.com/rs/zerolog

replace github.com/rs/zerolog/log => ./third_party/github.com/rs/zerolog/log
