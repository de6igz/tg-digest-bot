package pgx

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"
)

// TxOptions повторяет структуру из реального pgx.
type TxOptions struct{}

// CommandTag содержит результат DML-операции.
type CommandTag struct {
	RowsAffected int64
}

// BatchItem описывает один запрос в батче.
type BatchItem struct {
	Query string
	Args  []any
}

// Batch аккумулирует несколько операций для SendBatch.
type Batch struct {
	items []BatchItem
}

// Queue добавляет запрос в батч.
func (b *Batch) Queue(query string, args ...any) {
	if b == nil {
		return
	}
	cp := make([]any, len(args))
	copy(cp, args)
	b.items = append(b.items, BatchItem{Query: query, Args: cp})
}

// Items возвращает накопленные элементы (используется адаптерами).
func (b *Batch) Items() []BatchItem {
	if b == nil {
		return nil
	}
	out := make([]BatchItem, len(b.items))
	copy(out, b.items)
	return out
}

type BatchExecResult struct {
	Tag CommandTag
	Err error
}

// BatchResults хранит результаты SendBatch.
type BatchResults struct {
	results []BatchExecResult
	idx     int
}

// NewBatchResults создаёт последовательность результатов.
func NewBatchResults(res []BatchExecResult) *BatchResults {
	return &BatchResults{results: res, idx: 0}
}

// Exec возвращает следующий результат.
func (r *BatchResults) Exec() (CommandTag, error) {
	if r == nil || r.idx >= len(r.results) {
		return CommandTag{}, errors.New("no batch results")
	}
	res := r.results[r.idx]
	r.idx++
	return res.Tag, res.Err
}

// Close освобождает ресурсы (заглушка).
func (r *BatchResults) Close() error { return nil }

// Row имитирует pgx.Row.
type Row struct {
	Values []any
	Err    error
}

// Rows имитирует pgx.Rows.
type Rows struct {
	rows [][]any
	idx  int
	ErrV error
}

// NewRows создаёт коллекцию строк.
func NewRows(rows [][]any, err error) *Rows {
	r := &Rows{rows: rows, idx: 0}
	if err != nil {
		r.ErrV = err
	}
	return r
}

// ErrNoRows возвращается при отсутствии данных.
var ErrNoRows = errors.New("no rows")

// Scan копирует значения в dest.
func (r *Row) Scan(dest ...any) error {
	if r == nil {
		return ErrNoRows
	}
	if r.Err != nil {
		return r.Err
	}
	if len(dest) != len(r.Values) {
		return fmt.Errorf("scan mismatch: want %d got %d", len(dest), len(r.Values))
	}
	for i := range dest {
		if err := assign(dest[i], r.Values[i]); err != nil {
			return err
		}
	}
	return nil
}

// Next переходит к следующей строке.
func (r *Rows) Next() bool {
	if r == nil {
		return false
	}
	if r.idx >= len(r.rows) {
		return false
	}
	r.idx++
	return r.idx <= len(r.rows)
}

// Scan копирует значения текущей строки.
func (r *Rows) Scan(dest ...any) error {
	if r == nil {
		return ErrNoRows
	}
	if r.ErrV != nil {
		return r.ErrV
	}
	if r.idx == 0 || r.idx > len(r.rows) {
		return ErrNoRows
	}
	row := r.rows[r.idx-1]
	if len(dest) != len(row) {
		return fmt.Errorf("scan mismatch: want %d got %d", len(dest), len(row))
	}
	for i := range dest {
		if err := assign(dest[i], row[i]); err != nil {
			return err
		}
	}
	return nil
}

// Close освобождает ресурсы (ничего не делает).
func (r *Rows) Close() {}

// Err возвращает накопленную ошибку.
func (r *Rows) Err() error {
	if r == nil {
		return nil
	}
	return r.ErrV
}

// assign переносит значение в указатель-приёмник.
func assign(dest any, val any) error {
	if dest == nil {
		return errors.New("scan destination is nil")
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return errors.New("scan destination not a pointer")
	}
	switch d := dest.(type) {
	case *int64:
		*d = ToInt64(val)
	case *int:
		*d = int(ToInt64(val))
	case *string:
		if val == nil {
			*d = ""
		} else {
			*d = fmt.Sprint(val)
		}
	case *bool:
		if b, ok := val.(bool); ok {
			*d = b
		} else {
			*d = false
		}
	case *float64:
		switch v := val.(type) {
		case float64:
			*d = v
		case float32:
			*d = float64(v)
		case int64:
			*d = float64(v)
		case int:
			*d = float64(v)
		default:
			*d = 0
		}
	case *time.Time:
		if val == nil {
			*d = time.Time{}
		} else if t, ok := val.(time.Time); ok {
			*d = t
		} else if pt, ok := val.(*time.Time); ok {
			if pt != nil {
				*d = *pt
			} else {
				*d = time.Time{}
			}
		} else {
			*d = time.Time{}
		}
	case *sql.NullTime:
		if val == nil {
			d.Valid = false
		} else if nt, ok := val.(sql.NullTime); ok {
			*d = nt
		} else if t, ok := val.(time.Time); ok {
			d.Time = t
			d.Valid = true
		} else if pt, ok := val.(*time.Time); ok {
			if pt == nil {
				d.Valid = false
			} else {
				d.Time = *pt
				d.Valid = true
			}
		} else {
			d.Valid = false
		}
	case *[]byte:
		if val == nil {
			*d = nil
		} else if b, ok := val.([]byte); ok {
			buf := make([]byte, len(b))
			copy(buf, b)
			*d = buf
		} else if s, ok := val.(string); ok {
			*d = []byte(s)
		} else {
			*d = nil
		}
	default:
		if val == nil {
			rv.Elem().Set(reflect.Zero(rv.Elem().Type()))
		} else {
			v := reflect.ValueOf(val)
			if v.Type().AssignableTo(rv.Elem().Type()) {
				rv.Elem().Set(v)
			} else {
				return fmt.Errorf("cannot assign %T to %T", val, dest)
			}
		}
	}
	return nil
}

func ToInt64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case int32:
		return int64(t)
	case int:
		return int64(t)
	case uint:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		return int64(t)
	case float64:
		return int64(t)
	case float32:
		return int64(t)
	default:
		return 0
	}
}
