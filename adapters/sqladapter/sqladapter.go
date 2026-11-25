// Package sqladapter stores sesam's data in a SQL database through
// database/sql. The app opens the database with the driver of its choice and
// passes the *sql.DB; this package imports no driver.
//
// Tables get a prefix ("auth_" by default) so they can live next to the
// app's own tables. Migrate only adds tables, columns and indexes.
//
// SQLite: enable foreign keys and a busy timeout in the DSN, and either use
// one connection (db.SetMaxOpenConns(1)) or start transactions with
// BEGIN IMMEDIATE (modernc.org/sqlite: "_txlock=immediate"), so that two
// transactions never deadlock while upgrading to a write lock.
package sqladapter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/schema"
)

// Options change how the adapter names things.
type Options struct {
	// TablePrefix is put before every table name. Default "auth_"; set
	// NoPrefix to use the names as declared.
	TablePrefix string
	NoPrefix    bool
}

type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Adapter is an adapter.Adapter over database/sql.
type Adapter struct {
	db     *sql.DB
	q      querier // db, or the transaction
	inTx   bool
	d      dialect
	prefix string
	tables map[string]schema.Table
	order  []schema.Table
}

var _ adapter.Adapter = (*Adapter)(nil)

var prefixRe = regexp.MustCompile(`^[a-z0-9_]{0,20}$`)

// New returns an adapter for db in the given dialect.
func New(db *sql.DB, d Dialect, opts ...Options) (*Adapter, error) {
	if db == nil {
		return nil, errors.New("sqladapter: db is nil")
	}
	if d.d == nil {
		return nil, errors.New("sqladapter: no dialect, use sqladapter.SQLite or sqladapter.Postgres")
	}
	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}
	prefix := o.TablePrefix
	if prefix == "" && !o.NoPrefix {
		prefix = "auth_"
	}
	if !prefixRe.MatchString(prefix) {
		return nil, fmt.Errorf("sqladapter: invalid table prefix %q", prefix)
	}
	return &Adapter{db: db, q: db, d: d.d, prefix: prefix}, nil
}

func (a *Adapter) Schema(tables []schema.Table) error {
	if a.tables != nil {
		return errors.New("sqladapter: Schema called twice")
	}
	a.tables = map[string]schema.Table{}
	for _, t := range tables {
		a.tables[t.Name] = t
	}
	a.order = slices.Clone(tables)
	return nil
}

func (a *Adapter) table(name string) (schema.Table, error) {
	if a.tables == nil {
		return schema.Table{}, errors.New("sqladapter: Schema not called")
	}
	t, ok := a.tables[name]
	if !ok {
		return t, fmt.Errorf("sqladapter: unknown table %q", name)
	}
	return t, nil
}

func quote(name string) string { return `"` + name + `"` }

func (a *Adapter) tableName(name string) string { return quote(a.prefix + name) }

// DDL returns the statements that create all tables and indexes in an empty
// database, for apps that manage their migrations themselves.
func (a *Adapter) DDL() ([]string, error) {
	if a.tables == nil {
		return nil, errors.New("sqladapter: Schema not called")
	}
	var out []string
	for _, t := range a.order {
		out = append(out, a.createTable(t))
		out = append(out, a.createIndexes(t)...)
	}
	return out, nil
}

func (a *Adapter) column(f schema.Field) string {
	var b strings.Builder
	b.WriteString(quote(f.Name) + " " + a.d.columnType(f.Type))
	if f.Required {
		b.WriteString(" NOT NULL")
	}
	if f.Ref != nil {
		b.WriteString(" REFERENCES " + a.tableName(f.Ref.Table) + "(" + quote("id") + ")")
	}
	return b.String()
}

func (a *Adapter) createTable(t schema.Table) string {
	cols := []string{quote("id") + " TEXT NOT NULL PRIMARY KEY"}
	for _, f := range t.Fields {
		cols = append(cols, a.column(f))
	}
	return "CREATE TABLE IF NOT EXISTS " + a.tableName(t.Name) + " (\n  " + strings.Join(cols, ",\n  ") + "\n)"
}

func (a *Adapter) createIndexes(t schema.Table) []string {
	var out []string
	add := func(fields []string, unique bool) {
		suffix := "_idx"
		kind := "INDEX"
		if unique {
			suffix = "_key"
			kind = "UNIQUE INDEX"
		}
		name := indexName(a.prefix+t.Name+"_"+strings.Join(fields, "_"), suffix)
		cols := make([]string, len(fields))
		for i, f := range fields {
			cols[i] = quote(f)
		}
		out = append(out, fmt.Sprintf("CREATE %s IF NOT EXISTS %s ON %s (%s)", kind, quote(name), a.tableName(t.Name), strings.Join(cols, ", ")))
	}
	for _, f := range t.Fields {
		switch {
		case f.Unique:
			add([]string{f.Name}, true)
		case f.Index || f.Ref != nil:
			add([]string{f.Name}, false)
		}
	}
	for _, ix := range t.Indexes {
		add(ix.Fields, ix.Unique)
	}
	return out
}

// indexName keeps names within Postgres' 63 bytes.
func indexName(base, suffix string) string {
	if len(base)+len(suffix) <= 63 {
		return base + suffix
	}
	sum := sha256.Sum256([]byte(base))
	return base[:63-len(suffix)-9] + "_" + hex.EncodeToString(sum[:4]) + suffix
}

// migrateLock is the Postgres advisory lock key that serializes migrations
// of several app instances starting at once ("sesam" in ASCII).
const migrateLock = 0x736573616d

func (a *Adapter) Migrate(ctx context.Context) error {
	if a.inTx {
		return errors.New("sqladapter: Migrate in a transaction")
	}
	if a.tables == nil {
		return errors.New("sqladapter: Schema not called")
	}
	return a.Tx(ctx, func(txa adapter.Adapter) error {
		tx := txa.(*Adapter)
		if _, ok := a.d.(postgresDialect); ok {
			if _, err := tx.q.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", int64(migrateLock)); err != nil {
				return fmt.Errorf("sqladapter: lock for migration: %w", err)
			}
		}
		for _, t := range a.order {
			have, err := tx.columns(ctx, a.prefix+t.Name)
			if err != nil {
				return err
			}
			if len(have) == 0 {
				if _, err := tx.q.ExecContext(ctx, a.createTable(t)); err != nil {
					return fmt.Errorf("sqladapter: create table %s: %w", t.Name, err)
				}
			} else {
				for _, f := range t.Fields {
					if have[f.Name] {
						continue
					}
					if f.Required {
						return fmt.Errorf("sqladapter: cannot add required field %s.%s to an existing table", t.Name, f.Name)
					}
					stmt := "ALTER TABLE " + a.tableName(t.Name) + " ADD COLUMN " + a.column(f)
					if _, err := tx.q.ExecContext(ctx, stmt); err != nil {
						return fmt.Errorf("sqladapter: add field %s.%s: %w", t.Name, f.Name, err)
					}
				}
			}
			for _, stmt := range a.createIndexes(t) {
				if _, err := tx.q.ExecContext(ctx, stmt); err != nil {
					return fmt.Errorf("sqladapter: index on %s: %w", t.Name, err)
				}
			}
		}
		return nil
	})
}

func (a *Adapter) columns(ctx context.Context, table string) (map[string]bool, error) {
	rows, err := a.q.QueryContext(ctx, a.d.columnsQuery(), table)
	if err != nil {
		return nil, fmt.Errorf("sqladapter: read columns of %s: %w", table, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (a *Adapter) Tx(ctx context.Context, fn func(adapter.Adapter) error) error {
	if a.inTx {
		return fn(a)
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	child := *a
	child.q = tx
	child.inTx = true
	if err := fn(&child); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (a *Adapter) wrap(err error) error {
	if err != nil && a.d.isConflict(err) {
		return adapter.ErrConflict
	}
	return err
}

// args collects query arguments and hands out placeholders.
type args struct {
	d    dialect
	vals []any
}

func (p *args) add(v any) string {
	p.vals = append(p.vals, v)
	return p.d.placeholder(len(p.vals))
}

func (a *Adapter) where(t schema.Table, where []adapter.Where, p *args) (string, error) {
	where = slices.Clone(where)
	if err := adapter.CheckWhere(t, where); err != nil {
		return "", err
	}
	if len(where) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(where))
	for _, w := range where {
		f, _ := t.Field(w.Field)
		col := quote(w.Field)
		enc := func(v any) any { return a.d.encode(f.Type, v) }
		var s string
		switch w.Op {
		case adapter.OpEq:
			if w.Value == nil {
				s = col + " IS NULL"
			} else {
				s = col + " = " + p.add(enc(w.Value))
			}
		case adapter.OpNe:
			if w.Value == nil {
				s = col + " IS NOT NULL"
			} else {
				s = col + " <> " + p.add(enc(w.Value))
			}
		case adapter.OpLt:
			s = col + " < " + p.add(enc(w.Value))
		case adapter.OpLte:
			s = col + " <= " + p.add(enc(w.Value))
		case adapter.OpGt:
			s = col + " > " + p.add(enc(w.Value))
		case adapter.OpGte:
			s = col + " >= " + p.add(enc(w.Value))
		case adapter.OpIn:
			list := w.Value.([]any)
			if len(list) == 0 {
				s = "1 = 0"
				break
			}
			ph := make([]string, len(list))
			for i, v := range list {
				ph[i] = p.add(enc(v))
			}
			s = col + " IN (" + strings.Join(ph, ", ") + ")"
		case adapter.OpPrefix:
			// substr instead of LIKE: no escaping, and case-sensitive in
			// SQLite as well. The length is a number we computed.
			prefix := w.Value.(string)
			s = fmt.Sprintf("substr(%s, 1, %d) = %s", col, utf8.RuneCountInString(prefix), p.add(prefix))
		}
		parts = append(parts, s)
	}
	return " WHERE " + strings.Join(parts, " AND "), nil
}

func (a *Adapter) Create(ctx context.Context, table string, row adapter.Row) error {
	t, err := a.table(table)
	if err != nil {
		return err
	}
	r := make(adapter.Row, len(row))
	for k, v := range row {
		r[k] = v
	}
	if err := adapter.CheckRow(t, r, true); err != nil {
		return err
	}
	p := &args{d: a.d}
	var cols, ph []string
	for _, c := range t.Columns() {
		v, ok := r[c]
		if !ok {
			continue
		}
		f, _ := t.Field(c)
		cols = append(cols, quote(c))
		ph = append(ph, p.add(a.d.encode(f.Type, v)))
	}
	q := "INSERT INTO " + a.tableName(table) + " (" + strings.Join(cols, ", ") + ") VALUES (" + strings.Join(ph, ", ") + ")"
	_, err = a.q.ExecContext(ctx, q, p.vals...)
	return a.wrap(err)
}

func (a *Adapter) selectCols(t schema.Table) string {
	cols := t.Columns()
	for i, c := range cols {
		cols[i] = quote(c)
	}
	return strings.Join(cols, ", ")
}

func (a *Adapter) scan(t schema.Table, rows *sql.Rows) ([]adapter.Row, error) {
	defer rows.Close()
	cols := t.Columns()
	var out []adapter.Row
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(adapter.Row, len(cols))
		for i, c := range cols {
			f, _ := t.Field(c)
			v, err := a.d.decode(f.Type, raw[i])
			if err != nil {
				return nil, fmt.Errorf("%s.%s: %w", t.Name, c, err)
			}
			r[c] = v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (a *Adapter) FindOne(ctx context.Context, table string, where ...adapter.Where) (adapter.Row, error) {
	rows, err := a.FindMany(ctx, table, adapter.Query{Where: where, OrderBy: "id", Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, adapter.ErrNotFound
	}
	return rows[0], nil
}

func (a *Adapter) FindMany(ctx context.Context, table string, q adapter.Query) ([]adapter.Row, error) {
	t, err := a.table(table)
	if err != nil {
		return nil, err
	}
	p := &args{d: a.d}
	w, err := a.where(t, q.Where, p)
	if err != nil {
		return nil, err
	}
	s := "SELECT " + a.selectCols(t) + " FROM " + a.tableName(table) + w
	if q.OrderBy != "" {
		if _, ok := t.Field(q.OrderBy); !ok {
			return nil, fmt.Errorf("sqladapter: unknown field %s.%s", table, q.OrderBy)
		}
		// nil sorts first, as in every adapter.
		dir := " ASC NULLS FIRST"
		if q.Desc {
			dir = " DESC NULLS LAST"
		}
		s += " ORDER BY " + quote(q.OrderBy) + dir
		if q.OrderBy != "id" {
			s += ", " + quote("id") + " ASC"
		}
	}
	switch {
	case q.Limit > 0:
		s += fmt.Sprintf(" LIMIT %d", q.Limit)
	case q.Offset > 0:
		s += " LIMIT " + a.d.noLimit()
	}
	if q.Offset > 0 {
		s += fmt.Sprintf(" OFFSET %d", q.Offset)
	}
	rows, err := a.q.QueryContext(ctx, s, p.vals...)
	if err != nil {
		return nil, err
	}
	return a.scan(t, rows)
}

func (a *Adapter) Update(ctx context.Context, table string, set adapter.Row, where ...adapter.Where) (int64, error) {
	t, err := a.table(table)
	if err != nil {
		return 0, err
	}
	r := make(adapter.Row, len(set))
	for k, v := range set {
		r[k] = v
	}
	if err := adapter.CheckRow(t, r, false); err != nil {
		return 0, err
	}
	if len(r) == 0 {
		return a.Count(ctx, table, where...)
	}
	p := &args{d: a.d}
	names := make([]string, 0, len(r))
	for k := range r {
		names = append(names, k)
	}
	slices.Sort(names)
	assign := make([]string, len(names))
	for i, k := range names {
		f, _ := t.Field(k)
		if inc, ok := r[k].(adapter.Incr); ok {
			assign[i] = fmt.Sprintf("%s = COALESCE(%s, 0) + %s", quote(k), quote(k), p.add(int64(inc)))
			continue
		}
		assign[i] = quote(k) + " = " + p.add(a.d.encode(f.Type, r[k]))
	}
	w, err := a.where(t, where, p)
	if err != nil {
		return 0, err
	}
	res, err := a.q.ExecContext(ctx, "UPDATE "+a.tableName(table)+" SET "+strings.Join(assign, ", ")+w, p.vals...)
	if err != nil {
		return 0, a.wrap(err)
	}
	return res.RowsAffected()
}

func (a *Adapter) Delete(ctx context.Context, table string, where ...adapter.Where) (int64, error) {
	t, err := a.table(table)
	if err != nil {
		return 0, err
	}
	p := &args{d: a.d}
	w, err := a.where(t, where, p)
	if err != nil {
		return 0, err
	}
	res, err := a.q.ExecContext(ctx, "DELETE FROM "+a.tableName(table)+w, p.vals...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (a *Adapter) Count(ctx context.Context, table string, where ...adapter.Where) (int64, error) {
	t, err := a.table(table)
	if err != nil {
		return 0, err
	}
	p := &args{d: a.d}
	w, err := a.where(t, where, p)
	if err != nil {
		return 0, err
	}
	var n int64
	err = a.q.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+a.tableName(table)+w, p.vals...).Scan(&n)
	return n, err
}
