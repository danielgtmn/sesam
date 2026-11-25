package sqladapter

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtmn/sesam/schema"
)

// Dialect is the SQL flavour of a database.
type Dialect struct{ d dialect }

// The supported dialects.
var (
	// SQLite, e.g. with modernc.org/sqlite or github.com/mattn/go-sqlite3.
	// Times are stored as INTEGER microseconds since 1970, bools as 0/1.
	SQLite = Dialect{sqliteDialect{}}
	// Postgres, e.g. with github.com/jackc/pgx/v5/stdlib.
	Postgres = Dialect{postgresDialect{}}
)

func (d Dialect) String() string {
	if d.d == nil {
		return "none"
	}
	return d.d.name()
}

type dialect interface {
	name() string
	placeholder(n int) string // n starts at 1
	columnType(t schema.Type) string
	// encode turns a canonical value into what the driver stores.
	encode(t schema.Type, v any) any
	// decode turns what the driver returns into the canonical value.
	decode(t schema.Type, v any) (any, error)
	isConflict(err error) bool
	// columnsQuery lists the column names of a table, one argument: the name.
	columnsQuery() string
	noLimit() string
}

type sqliteDialect struct{}

func (sqliteDialect) name() string           { return "sqlite" }
func (sqliteDialect) placeholder(int) string { return "?" }
func (sqliteDialect) noLimit() string        { return "-1" }
func (sqliteDialect) columnsQuery() string   { return `SELECT name FROM pragma_table_info(?)` }

func (sqliteDialect) columnType(t schema.Type) string {
	if t == schema.String {
		return "TEXT"
	}
	return "INTEGER"
}

func (sqliteDialect) encode(t schema.Type, v any) any {
	switch x := v.(type) {
	case bool:
		if x {
			return int64(1)
		}
		return int64(0)
	case time.Time:
		return x.UnixMicro()
	}
	return v
}

func (sqliteDialect) decode(t schema.Type, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	switch t {
	case schema.String:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case schema.Int, schema.Bool, schema.Time:
		n, ok := v.(int64)
		if !ok {
			break
		}
		switch t {
		case schema.Bool:
			return n != 0, nil
		case schema.Time:
			return time.UnixMicro(n).UTC(), nil
		}
		return n, nil
	}
	return nil, fmt.Errorf("sqladapter: cannot read %T as %s", v, t)
}

// SQLite extended result codes for unique and primary key violations.
const (
	sqliteConstraintUnique     = 2067
	sqliteConstraintPrimaryKey = 1555
)

func (sqliteDialect) isConflict(err error) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() {
		case sqliteConstraintUnique, sqliteConstraintPrimaryKey:
			return true
		}
	}
	// Drivers without a Code method (e.g. mattn/go-sqlite3) still say so.
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

type postgresDialect struct{}

func (postgresDialect) name() string             { return "postgres" }
func (postgresDialect) placeholder(n int) string { return fmt.Sprintf("$%d", n) }
func (postgresDialect) noLimit() string          { return "ALL" }
func (postgresDialect) columnsQuery() string {
	return `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1`
}

func (postgresDialect) columnType(t schema.Type) string {
	switch t {
	case schema.Int:
		return "BIGINT"
	case schema.Bool:
		return "BOOLEAN"
	case schema.Time:
		return "TIMESTAMPTZ"
	}
	return "TEXT"
}

func (postgresDialect) encode(t schema.Type, v any) any { return v }

func (postgresDialect) decode(t schema.Type, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if b, ok := v.([]byte); ok {
		v = string(b)
	}
	switch t {
	case schema.String:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case schema.Int:
		switch n := v.(type) {
		case int64:
			return n, nil
		case int32:
			return int64(n), nil
		}
	case schema.Bool:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case schema.Time:
		if tm, ok := v.(time.Time); ok {
			return tm.UTC(), nil
		}
	}
	return nil, fmt.Errorf("sqladapter: cannot read %T as %s", v, t)
}

func (postgresDialect) isConflict(err error) bool {
	// pgx (*pgconn.PgError) and lib/pq (*pq.Error) both report SQLSTATE.
	var coded interface{ SQLState() string }
	if errors.As(err, &coded) {
		return coded.SQLState() == "23505"
	}
	return strings.Contains(err.Error(), "SQLSTATE 23505")
}
