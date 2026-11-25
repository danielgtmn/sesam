// Package adapter defines how sesam talks to a database. An adapter stores
// rows of the tables declared with package schema and answers simple
// queries; it never needs to know what the rows mean. Implementations live in
// sesam/adapters; adapters/adaptertest checks that one behaves correctly.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtmn/sesam/schema"
)

// Errors every adapter returns for the same situations.
var (
	// ErrNotFound: FindOne matched no row.
	ErrNotFound = errors.New("adapter: not found")
	// ErrConflict: a write broke a unique field, unique index or the
	// primary key.
	ErrConflict = errors.New("adapter: conflict")
)

// Row is a row by field name. Values have the Go types listed in package
// schema; a field missing from a Row written with Create is stored as nil.
type Row map[string]any

// String returns the string in field name, or "" if it is nil.
func (r Row) String(name string) string { s, _ := r[name].(string); return s }

// Int returns the int64 in field name, or 0 if it is nil.
func (r Row) Int(name string) int64 { n, _ := r[name].(int64); return n }

// Bool returns the bool in field name, or false if it is nil.
func (r Row) Bool(name string) bool { b, _ := r[name].(bool); return b }

// Time returns the time in field name, or the zero time if it is nil.
func (r Row) Time(name string) time.Time { t, _ := r[name].(time.Time); return t }

// Op is a comparison in a Where.
type Op int

// Comparisons. Eq and Ne with a nil value test for NULL. In takes a []any.
// Prefix matches strings that start with the value, case-sensitively.
const (
	OpEq Op = iota + 1
	OpNe
	OpLt
	OpLte
	OpGt
	OpGte
	OpIn
	OpPrefix
)

// Where is one condition; several are combined with AND.
type Where struct {
	Field string
	Op    Op
	Value any
}

func Eq(field string, v any) Where    { return Where{field, OpEq, v} }
func Ne(field string, v any) Where    { return Where{field, OpNe, v} }
func Lt(field string, v any) Where    { return Where{field, OpLt, v} }
func Lte(field string, v any) Where   { return Where{field, OpLte, v} }
func Gt(field string, v any) Where    { return Where{field, OpGt, v} }
func Gte(field string, v any) Where   { return Where{field, OpGte, v} }
func In(field string, v ...any) Where { return Where{field, OpIn, v} }
func Prefix(field, p string) Where    { return Where{field, OpPrefix, p} }

// Query selects rows for FindMany. Without OrderBy the order is undefined.
// Limit 0 means no limit.
type Query struct {
	Where   []Where
	OrderBy string
	Desc    bool
	Limit   int
	Offset  int
}

// Incr, used as a value in Update, adds to an Int field atomically instead
// of overwriting it. A nil field counts as 0.
type Incr int64

// Adapter stores rows. Table names are logical (as declared); field names
// must be declared in the table. All methods are safe for concurrent use.
type Adapter interface {
	// Schema tells the adapter which tables exist. It must be called once,
	// before any other method; it does not touch the database.
	Schema(tables []schema.Table) error
	// Migrate creates missing tables, fields and indexes. It only adds,
	// never changes or drops, and can run any number of times.
	Migrate(ctx context.Context) error

	Create(ctx context.Context, table string, row Row) error
	FindOne(ctx context.Context, table string, where ...Where) (Row, error)
	FindMany(ctx context.Context, table string, q Query) ([]Row, error)
	// Update changes the fields in set on every matching row and returns
	// how many matched.
	Update(ctx context.Context, table string, set Row, where ...Where) (int64, error)
	Delete(ctx context.Context, table string, where ...Where) (int64, error)
	Count(ctx context.Context, table string, where ...Where) (int64, error)

	// Tx runs fn in a transaction: all its writes happen or none. fn must
	// use the Adapter it is given, not the outer one. Calling Tx inside fn
	// runs in the same transaction. When a call inside fn fails, fn must
	// return: some databases (Postgres) cannot continue the transaction.
	Tx(ctx context.Context, fn func(tx Adapter) error) error
}

// Value checks v against field f and returns it in the canonical Go type:
// any integer becomes int64, a time becomes UTC with microsecond precision.
// nil is allowed if nullOK. Adapters call it for every value they receive.
func Value(f schema.Field, v any, nullOK bool) (any, error) {
	if v == nil {
		if !nullOK {
			return nil, fmt.Errorf("adapter: field %q must not be nil", f.Name)
		}
		return nil, nil
	}
	switch f.Type {
	case schema.String:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case schema.Int:
		switch n := v.(type) {
		case int:
			return int64(n), nil
		case int32:
			return int64(n), nil
		case int64:
			return n, nil
		}
	case schema.Bool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case schema.Time:
		if t, ok := v.(time.Time); ok {
			return NormalizeTime(t), nil
		}
	}
	return nil, fmt.Errorf("adapter: field %q is %s, got %T", f.Name, f.Type, v)
}

// NormalizeTime is how times are stored: UTC, microseconds, no monotonic
// clock reading.
func NormalizeTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond).Round(0) }

// CheckWhere validates conditions against a table and normalizes their
// values in place.
func CheckWhere(t schema.Table, where []Where) error {
	for i, w := range where {
		f, ok := t.Field(w.Field)
		if !ok {
			return fmt.Errorf("adapter: unknown field %s.%s", t.Name, w.Field)
		}
		switch w.Op {
		case OpEq, OpNe:
			v, err := Value(f, w.Value, true)
			if err != nil {
				return err
			}
			where[i].Value = v
		case OpLt, OpLte, OpGt, OpGte:
			if f.Type == schema.Bool {
				return fmt.Errorf("adapter: cannot order bool field %s.%s", t.Name, w.Field)
			}
			v, err := Value(f, w.Value, false)
			if err != nil {
				return err
			}
			where[i].Value = v
		case OpIn:
			list, ok := w.Value.([]any)
			if !ok {
				return fmt.Errorf("adapter: In on %s.%s needs a []any", t.Name, w.Field)
			}
			out := make([]any, len(list))
			for j, x := range list {
				v, err := Value(f, x, false)
				if err != nil {
					return err
				}
				out[j] = v
			}
			where[i].Value = out
		case OpPrefix:
			if f.Type != schema.String {
				return fmt.Errorf("adapter: Prefix on non-string field %s.%s", t.Name, w.Field)
			}
			if _, ok := w.Value.(string); !ok {
				return fmt.Errorf("adapter: Prefix on %s.%s needs a string", t.Name, w.Field)
			}
		default:
			return fmt.Errorf("adapter: invalid operator %d", w.Op)
		}
	}
	return nil
}

// CheckRow validates the fields of a row to write against a table and
// normalizes the values in place. For Create (full) every Required field
// must be present and "id" must be a non-empty string; for Update, "id"
// cannot be changed and Incr is allowed on Int fields.
func CheckRow(t schema.Table, row Row, full bool) error {
	for name, v := range row {
		f, ok := t.Field(name)
		if !ok {
			return fmt.Errorf("adapter: unknown field %s.%s", t.Name, name)
		}
		if !full && name == "id" {
			return fmt.Errorf("adapter: cannot update the id of %s", t.Name)
		}
		if inc, ok := v.(Incr); ok {
			if full || f.Type != schema.Int {
				return fmt.Errorf("adapter: Incr only in Update of int fields (%s.%s)", t.Name, name)
			}
			row[name] = inc
			continue
		}
		nv, err := Value(f, v, !f.Required)
		if err != nil {
			return err
		}
		row[name] = nv
	}
	if full {
		if id, _ := row["id"].(string); id == "" {
			return fmt.Errorf("adapter: row for %s needs an id", t.Name)
		}
		for _, f := range t.Fields {
			if _, ok := row[f.Name]; f.Required && !ok {
				return fmt.Errorf("adapter: missing required field %s.%s", t.Name, f.Name)
			}
		}
	}
	return nil
}
