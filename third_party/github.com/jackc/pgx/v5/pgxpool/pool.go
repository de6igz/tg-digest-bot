package pgxpool

import (
"context"

"github.com/jackc/pgx/v5"
)

type Pool struct{}

type Config struct {
MaxConns int32
}

type Tx struct{}

func ParseConfig(dsn string) (*Config, error) {
return &Config{}, nil
}

func NewWithConfig(ctx context.Context, cfg *Config) (*Pool, error) {
_ = ctx
_ = cfg
return &Pool{}, nil
}

func (p *Pool) Close() {}

func (p *Pool) BeginTx(ctx context.Context, opts pgx.TxOptions) (*Tx, error) {
return &Tx{}, nil
}

func (t *Tx) Rollback(ctx context.Context) error { return nil }
func (t *Tx) Commit(ctx context.Context) error   { return nil }
func (t *Tx) Exec(ctx context.Context, query string, args ...any) (pgx.CommandTag, error) {
return pgx.CommandTag{}, nil
}
func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *pgx.Row {
return &pgx.Row{}
}

func (p *Pool) QueryRow(ctx context.Context, query string, args ...any) *pgx.Row {
return &pgx.Row{}
}

func (p *Pool) Exec(ctx context.Context, query string, args ...any) (pgx.CommandTag, error) {
return pgx.CommandTag{}, nil
}

func (p *Pool) Query(ctx context.Context, query string, args ...any) (*pgx.Rows, error) {
return &pgx.Rows{}, nil
}

func (p *Pool) SendBatch(ctx context.Context, batch *pgx.Batch) *pgx.BatchResults {
return &pgx.BatchResults{}
}
