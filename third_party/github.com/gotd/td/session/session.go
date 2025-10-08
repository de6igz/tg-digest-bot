package session

import (
	"context"
	"errors"
)

// ErrNotFound соответствует ошибке отсутствия сохранённой сессии в реальной библиотеке gotd.
var ErrNotFound = errors.New("session not found")

type Storage interface {
	LoadSession(ctx context.Context) ([]byte, error)
	StoreSession(ctx context.Context, data []byte) error
}
