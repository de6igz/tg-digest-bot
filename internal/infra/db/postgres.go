package db

import (
"context"
"time"

"github.com/jackc/pgx/v5/pgxpool"
)

// Connect создаёт пул подключений к Postgres.
func Connect(dsn string) (*pgxpool.Pool, error) {
cfg, err := pgxpool.ParseConfig(dsn)
if err != nil {
return nil, err
}
cfg.MaxConns = 5
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
pool, err := pgxpool.NewWithConfig(ctx, cfg)
if err != nil {
return nil, err
}
return pool, nil
}
