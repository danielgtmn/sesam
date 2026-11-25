// Package memadapter keeps sesam's data in memory. It is meant for tests of
// plugins and of apps that use sesam; everything is lost when the process
// ends. Transactions are serialized: while one runs, other calls wait.
package memadapter

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/schema"
)

// Adapter is an in-memory adapter.Adapter.
type Adapter struct {
	mu     sync.Mutex
	tables map[string]schema.Table
	data   map[string]map[string]adapter.Row // table -> id -> row
}

// New returns an empty in-memory adapter.
func New() *Adapter { return &Adapter{} }

var _ adapter.Adapter = (*Adapter)(nil)

func (a *Adapter) Schema(tables []schema.Table) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tables != nil {
		return fmt.Errorf("memadapter: Schema called twice")
	}
	a.tables = map[string]schema.Table{}
	a.data = map[string]map[string]adapter.Row{}
	for _, t := range tables {
		a.tables[t.Name] = t
		a.data[t.Name] = map[string]adapter.Row{}
	}
	return nil
}

func (a *Adapter) Migrate(context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tables == nil {
		return fmt.Errorf("memadapter: Schema not called")
	}
	return nil
}

func (a *Adapter) Create(ctx context.Context, table string, row adapter.Row) error {
	return a.do(func(s *state) error { return s.create(table, row) })
}

func (a *Adapter) FindOne(ctx context.Context, table string, where ...adapter.Where) (adapter.Row, error) {
	var out adapter.Row
	err := a.do(func(s *state) (err error) { out, err = s.findOne(table, where); return })
	return out, err
}

func (a *Adapter) FindMany(ctx context.Context, table string, q adapter.Query) ([]adapter.Row, error) {
	var out []adapter.Row
	err := a.do(func(s *state) (err error) { out, err = s.findMany(table, q); return })
	return out, err
}

func (a *Adapter) Update(ctx context.Context, table string, set adapter.Row, where ...adapter.Where) (int64, error) {
	var n int64
	err := a.do(func(s *state) (err error) { n, err = s.update(table, set, where); return })
	return n, err
}

func (a *Adapter) Delete(ctx context.Context, table string, where ...adapter.Where) (int64, error) {
	var n int64
	err := a.do(func(s *state) (err error) { n, err = s.delete(table, where); return })
	return n, err
}

func (a *Adapter) Count(ctx context.Context, table string, where ...adapter.Where) (int64, error) {
	var n int64
	err := a.do(func(s *state) (err error) {
		rows, err := s.match(table, where)
		n = int64(len(rows))
		return err
	})
	return n, err
}

// Tx holds the lock for the whole transaction and works on a copy, which
// replaces the data only if fn succeeds.
func (a *Adapter) Tx(ctx context.Context, fn func(adapter.Adapter) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tables == nil {
		return fmt.Errorf("memadapter: Schema not called")
	}
	tx := &txAdapter{s: &state{tables: a.tables, data: clone(a.data)}}
	if err := fn(tx); err != nil {
		return err
	}
	a.data = tx.s.data
	return nil
}

func (a *Adapter) do(fn func(*state) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tables == nil {
		return fmt.Errorf("memadapter: Schema not called")
	}
	// Every operation checks everything before it writes, so a failed one
	// changes nothing.
	s := &state{tables: a.tables, data: a.data}
	return fn(s)
}

func clone(d map[string]map[string]adapter.Row) map[string]map[string]adapter.Row {
	out := make(map[string]map[string]adapter.Row, len(d))
	for t, rows := range d {
		m := make(map[string]adapter.Row, len(rows))
		for id, r := range rows {
			m[id] = maps.Clone(r)
		}
		out[t] = m
	}
	return out
}

// txAdapter runs inside Tx; the lock is already held.
type txAdapter struct{ s *state }

func (t *txAdapter) Schema([]schema.Table) error {
	return fmt.Errorf("memadapter: Schema in a transaction")
}
func (t *txAdapter) Migrate(context.Context) error {
	return fmt.Errorf("memadapter: Migrate in a transaction")
}
func (t *txAdapter) Create(_ context.Context, table string, row adapter.Row) error {
	return t.s.create(table, row)
}
func (t *txAdapter) FindOne(_ context.Context, table string, where ...adapter.Where) (adapter.Row, error) {
	return t.s.findOne(table, where)
}
func (t *txAdapter) FindMany(_ context.Context, table string, q adapter.Query) ([]adapter.Row, error) {
	return t.s.findMany(table, q)
}
func (t *txAdapter) Update(_ context.Context, table string, set adapter.Row, where ...adapter.Where) (int64, error) {
	return t.s.update(table, set, where)
}
func (t *txAdapter) Delete(_ context.Context, table string, where ...adapter.Where) (int64, error) {
	return t.s.delete(table, where)
}
func (t *txAdapter) Count(_ context.Context, table string, where ...adapter.Where) (int64, error) {
	rows, err := t.s.match(table, where)
	return int64(len(rows)), err
}
func (t *txAdapter) Tx(_ context.Context, fn func(adapter.Adapter) error) error { return fn(t) }

type state struct {
	tables map[string]schema.Table
	data   map[string]map[string]adapter.Row
}

func (s *state) table(name string) (schema.Table, error) {
	t, ok := s.tables[name]
	if !ok {
		return t, fmt.Errorf("memadapter: unknown table %q", name)
	}
	return t, nil
}

func (s *state) create(table string, row adapter.Row) error {
	t, err := s.table(table)
	if err != nil {
		return err
	}
	r := maps.Clone(row)
	if err := adapter.CheckRow(t, r, true); err != nil {
		return err
	}
	for _, f := range t.Fields {
		if _, ok := r[f.Name]; !ok {
			r[f.Name] = nil
		}
	}
	id := r.String("id")
	if _, ok := s.data[table][id]; ok {
		return adapter.ErrConflict
	}
	if s.conflicts(t, r, "") {
		return adapter.ErrConflict
	}
	s.data[table][id] = r
	return nil
}

// conflicts tells whether r breaks a unique field or index against any row
// other than the one with id skip. Rows with a nil in the key never conflict.
func (s *state) conflicts(t schema.Table, r adapter.Row, skip string) bool {
	var keys [][]string
	for _, f := range t.Fields {
		if f.Unique {
			keys = append(keys, []string{f.Name})
		}
	}
	for _, ix := range t.Indexes {
		if ix.Unique {
			keys = append(keys, ix.Fields)
		}
	}
	for _, key := range keys {
		if slices.ContainsFunc(key, func(k string) bool { return r[k] == nil }) {
			continue
		}
		for id, o := range s.data[t.Name] {
			if id == skip {
				continue
			}
			if !slices.ContainsFunc(key, func(k string) bool { return !equal(o[k], r[k]) }) {
				return true
			}
		}
	}
	return false
}

func (s *state) findOne(table string, where []adapter.Where) (adapter.Row, error) {
	rows, err := s.match(table, where)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, adapter.ErrNotFound
	}
	// Deterministic when several match.
	slices.SortFunc(rows, func(a, b adapter.Row) int { return strings.Compare(a.String("id"), b.String("id")) })
	return maps.Clone(rows[0]), nil
}

func (s *state) findMany(table string, q adapter.Query) ([]adapter.Row, error) {
	t, err := s.table(table)
	if err != nil {
		return nil, err
	}
	rows, err := s.match(table, q.Where)
	if err != nil {
		return nil, err
	}
	if q.OrderBy != "" {
		if _, ok := t.Field(q.OrderBy); !ok {
			return nil, fmt.Errorf("memadapter: unknown field %s.%s", table, q.OrderBy)
		}
		slices.SortStableFunc(rows, func(a, b adapter.Row) int {
			c := compare(a[q.OrderBy], b[q.OrderBy])
			if q.Desc {
				c = -c
			}
			return c
		})
	}
	if q.Offset > 0 {
		rows = rows[min(q.Offset, len(rows)):]
	}
	if q.Limit > 0 && len(rows) > q.Limit {
		rows = rows[:q.Limit]
	}
	out := make([]adapter.Row, len(rows))
	for i, r := range rows {
		out[i] = maps.Clone(r)
	}
	return out, nil
}

func (s *state) update(table string, set adapter.Row, where []adapter.Where) (int64, error) {
	t, err := s.table(table)
	if err != nil {
		return 0, err
	}
	set = maps.Clone(set)
	if err := adapter.CheckRow(t, set, false); err != nil {
		return 0, err
	}
	rows, err := s.match(table, where)
	if err != nil {
		return 0, err
	}
	// Check every change first, so a conflict changes nothing.
	updated := make([]adapter.Row, len(rows))
	for i, r := range rows {
		n := maps.Clone(r)
		for k, v := range set {
			if inc, ok := v.(adapter.Incr); ok {
				old, _ := n[k].(int64)
				n[k] = old + int64(inc)
				continue
			}
			n[k] = v
		}
		updated[i] = n
	}
	trial := &state{tables: s.tables, data: clone(s.data)}
	for _, n := range updated {
		trial.data[table][n.String("id")] = n
	}
	for _, n := range updated {
		if trial.conflicts(t, n, n.String("id")) {
			return 0, adapter.ErrConflict
		}
	}
	for _, n := range updated {
		s.data[table][n.String("id")] = n
	}
	return int64(len(updated)), nil
}

func (s *state) delete(table string, where []adapter.Where) (int64, error) {
	rows, err := s.match(table, where)
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		delete(s.data[table], r.String("id"))
	}
	return int64(len(rows)), nil
}

// match returns the stored rows (not copies) that fulfil all conditions.
func (s *state) match(table string, where []adapter.Where) ([]adapter.Row, error) {
	t, err := s.table(table)
	if err != nil {
		return nil, err
	}
	where = slices.Clone(where)
	if err := adapter.CheckWhere(t, where); err != nil {
		return nil, err
	}
	var out []adapter.Row
	for _, r := range s.data[table] {
		if slices.ContainsFunc(where, func(w adapter.Where) bool { return !test(r[w.Field], w) }) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func test(v any, w adapter.Where) bool {
	switch w.Op {
	case adapter.OpEq:
		if w.Value == nil {
			return v == nil
		}
		return v != nil && equal(v, w.Value)
	case adapter.OpNe:
		if w.Value == nil {
			return v != nil
		}
		// Like SQL: NULL is neither equal nor unequal to a value.
		return v != nil && !equal(v, w.Value)
	case adapter.OpLt, adapter.OpLte, adapter.OpGt, adapter.OpGte:
		if v == nil {
			return false
		}
		c := compare(v, w.Value)
		switch w.Op {
		case adapter.OpLt:
			return c < 0
		case adapter.OpLte:
			return c <= 0
		case adapter.OpGt:
			return c > 0
		}
		return c >= 0
	case adapter.OpIn:
		return v != nil && slices.ContainsFunc(w.Value.([]any), func(x any) bool { return equal(v, x) })
	case adapter.OpPrefix:
		s, ok := v.(string)
		return ok && strings.HasPrefix(s, w.Value.(string))
	}
	return false
}

func equal(a, b any) bool {
	if ta, ok := a.(time.Time); ok {
		tb, ok := b.(time.Time)
		return ok && ta.Equal(tb)
	}
	return a == b
}

// compare orders values of one type; nil sorts first.
func compare(a, b any) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return -1
	case b == nil:
		return 1
	}
	switch x := a.(type) {
	case string:
		return strings.Compare(x, b.(string))
	case int64:
		return cmp.Compare(x, b.(int64))
	case time.Time:
		return x.Compare(b.(time.Time))
	case bool:
		y := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	}
	return 0
}
