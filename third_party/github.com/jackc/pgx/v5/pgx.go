package pgx

import (
    "errors"
    "time"
)

type TxOptions struct{}

type CommandTag struct{}

type Batch struct{}

type BatchResults struct{}

type Row struct{}

type Rows struct{}

type NullTime struct {
    Time  time.Time
    Valid bool
}

var ErrNoRows = errors.New("no rows")

func (b *Batch) Queue(query string, args ...any) {}

func (r *BatchResults) Exec() (CommandTag, error) {
return CommandTag{}, nil
}

func (r *BatchResults) Close() error { return nil }

func (r *Row) Scan(dest ...any) error {
return ErrNoRows
}

func (r *Rows) Close() {}
func (r *Rows) Err() error { return nil }
func (r *Rows) Next() bool  { return false }
func (r *Rows) Scan(dest ...any) error {
return ErrNoRows
}
