// Package adaptertest checks that an adapter.Adapter behaves the way sesam
// expects. Every adapter in this module runs it, and so should adapters
// written elsewhere:
//
//	func TestConformance(t *testing.T) {
//		adaptertest.Run(t, func(t *testing.T) adapter.Adapter { return newEmptyAdapter(t) })
//	}
//
// newAdapter must return an adapter over an empty database each time; Run
// calls Schema and Migrate itself.
package adaptertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/schema"
)

// Tables is the schema the tests use.
var Tables = []schema.Table{
	{Name: "parents", Fields: []schema.Field{
		{Name: "name", Type: schema.String, Required: true, Unique: true},
	}},
	{Name: "items", Fields: []schema.Field{
		{Name: "parent_id", Type: schema.String, Ref: &schema.Reference{Table: "parents"}},
		{Name: "title", Type: schema.String, Required: true},
		{Name: "code", Type: schema.String, Unique: true},
		{Name: "path", Type: schema.String, Index: true},
		{Name: "count", Type: schema.Int},
		{Name: "done", Type: schema.Bool},
		{Name: "at", Type: schema.Time},
	}, Indexes: []schema.Index{
		{Fields: []string{"parent_id", "title"}, Unique: true},
	}},
}

// Run runs every check as a subtest.
func Run(t *testing.T, newAdapter func(t *testing.T) adapter.Adapter) {
	t.Helper()
	setup := func(t *testing.T) adapter.Adapter {
		t.Helper()
		a := newAdapter(t)
		if err := a.Schema(Tables); err != nil {
			t.Fatalf("Schema: %v", err)
		}
		if err := a.Migrate(context.Background()); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
		return a
	}
	tests := []struct {
		name string
		fn   func(*testing.T, adapter.Adapter)
	}{
		{"RoundTrip", testRoundTrip},
		{"NotFound", testNotFound},
		{"Conflicts", testConflicts},
		{"Required", testRequired},
		{"UnknownNames", testUnknownNames},
		{"Operators", testOperators},
		{"OrderLimitOffset", testOrderLimitOffset},
		{"Update", testUpdate},
		{"UpdateConflictChangesNothing", testUpdateConflict},
		{"Incr", testIncr},
		{"Delete", testDelete},
		{"TxCommit", testTxCommit},
		{"TxRollback", testTxRollback},
		{"ConcurrentIncr", testConcurrentIncr},
		{"MigrateTwice", testMigrateTwice},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) { tc.fn(t, setup(t)) })
	}
	t.Run("MigrateAddsFields", func(t *testing.T) { testMigrateAddsFields(t, newAdapter) })
}

var ctx = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func parent(t *testing.T, a adapter.Adapter, id string) {
	t.Helper()
	must(t, a.Create(ctx, "parents", adapter.Row{"id": id, "name": "parent " + id}))
}

func testRoundTrip(t *testing.T, a adapter.Adapter) {
	parent(t, a, "p1")
	// A local time with nanoseconds and a monotonic reading comes back as
	// UTC with microseconds.
	at := time.Date(2026, 11, 25, 9, 30, 15, 123456789, time.FixedZone("CET", 3600))
	must(t, a.Create(ctx, "items", adapter.Row{
		"id": "i1", "parent_id": "p1", "title": "Ä title ✓", "code": "c1",
		"path": "/a/b/", "count": 42, "done": true, "at": at,
	}))
	must(t, a.Create(ctx, "items", adapter.Row{"id": "i2", "title": "nulls"}))

	got, err := a.FindOne(ctx, "items", adapter.Eq("id", "i1"))
	must(t, err)
	want := adapter.Row{
		"id": "i1", "parent_id": "p1", "title": "Ä title ✓", "code": "c1",
		"path": "/a/b/", "count": int64(42), "done": true,
		"at": time.Date(2026, 11, 25, 8, 30, 15, 123456000, time.UTC),
	}
	if !rowsEqual(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if got.Time("at").Location() != time.UTC {
		t.Errorf("time location = %v, want UTC", got.Time("at").Location())
	}

	got, err = a.FindOne(ctx, "items", adapter.Eq("id", "i2"))
	must(t, err)
	for _, f := range []string{"parent_id", "code", "path", "count", "done", "at"} {
		v, ok := got[f]
		if !ok || v != nil {
			t.Errorf("field %s = %v (present %v), want nil", f, v, ok)
		}
	}
	if got.Bool("done") {
		t.Error("nil bool not false")
	}
	// false is stored as false, not as nil.
	must(t, a.Create(ctx, "items", adapter.Row{"id": "i3", "title": "f", "done": false, "count": 0}))
	got, err = a.FindOne(ctx, "items", adapter.Eq("done", false))
	must(t, err)
	if got.String("id") != "i3" || got["count"] != int64(0) {
		t.Errorf("got %v", got)
	}
}

func testNotFound(t *testing.T, a adapter.Adapter) {
	_, err := a.FindOne(ctx, "items", adapter.Eq("id", "nope"))
	if !errors.Is(err, adapter.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	rows, err := a.FindMany(ctx, "items", adapter.Query{Where: []adapter.Where{adapter.Eq("id", "nope")}})
	if err != nil || len(rows) != 0 {
		t.Fatalf("FindMany = %v, %v", rows, err)
	}
}

func testConflicts(t *testing.T, a adapter.Adapter) {
	parent(t, a, "p1")
	must(t, a.Create(ctx, "items", adapter.Row{"id": "i1", "title": "t", "code": "c", "parent_id": "p1"}))
	cases := map[string]adapter.Row{
		"primary key":  {"id": "i1", "title": "other"},
		"unique field": {"id": "i2", "title": "other", "code": "c"},
		"unique index": {"id": "i3", "title": "t", "parent_id": "p1"},
	}
	for name, row := range cases {
		if err := a.Create(ctx, "items", row); !errors.Is(err, adapter.ErrConflict) {
			t.Errorf("%s: err = %v, want ErrConflict", name, err)
		}
	}
	// nil never conflicts, in a unique field or a unique index.
	must(t, a.Create(ctx, "items", adapter.Row{"id": "n1", "title": "t"}))
	must(t, a.Create(ctx, "items", adapter.Row{"id": "n2", "title": "t"}))
	if n, err := a.Count(ctx, "items"); err != nil || n != 3 {
		t.Fatalf("count = %d, %v; want 3", n, err)
	}
}

func testRequired(t *testing.T, a adapter.Adapter) {
	for name, row := range map[string]adapter.Row{
		"missing":  {"id": "x"},
		"nil":      {"id": "x", "title": nil},
		"no id":    {"title": "t"},
		"empty id": {"id": "", "title": "t"},
	} {
		if err := a.Create(ctx, "items", row); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	must(t, a.Create(ctx, "items", adapter.Row{"id": "x", "title": "t"}))
	if _, err := a.Update(ctx, "items", adapter.Row{"title": nil}, adapter.Eq("id", "x")); err == nil {
		t.Error("required field set to nil")
	}
	if _, err := a.Update(ctx, "items", adapter.Row{"id": "y"}, adapter.Eq("id", "x")); err == nil {
		t.Error("id changed")
	}
	if err := a.Create(ctx, "items", adapter.Row{"id": "z", "title": "t", "count": "3"}); err == nil {
		t.Error("string accepted for int")
	}
}

func testUnknownNames(t *testing.T, a adapter.Adapter) {
	if err := a.Create(ctx, "nope", adapter.Row{"id": "x"}); err == nil {
		t.Error("unknown table accepted")
	}
	if err := a.Create(ctx, "items", adapter.Row{"id": "x", "title": "t", "evil\"": 1}); err == nil {
		t.Error("unknown field accepted")
	}
	if _, err := a.FindOne(ctx, "items", adapter.Eq("title; DROP TABLE items", "x")); err == nil {
		t.Error("unknown field in where accepted")
	}
	if _, err := a.FindMany(ctx, "items", adapter.Query{OrderBy: "nope"}); err == nil {
		t.Error("unknown order field accepted")
	}
}

func seed(t *testing.T, a adapter.Adapter) {
	t.Helper()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rows := []adapter.Row{
		{"id": "a", "title": "a", "count": 1, "path": "/org/", "at": base},
		{"id": "b", "title": "b", "count": 2, "path": "/org/team/", "at": base.Add(time.Hour), "done": true},
		{"id": "c", "title": "c", "count": 3, "path": "/org/team/x/", "at": base.Add(2 * time.Hour)},
		{"id": "d", "title": "d", "count": 4, "path": "/Org/", "at": base.Add(3 * time.Hour)},
		{"id": "e", "title": "e"},
	}
	for _, r := range rows {
		must(t, a.Create(ctx, "items", r))
	}
}

func ids(t *testing.T, a adapter.Adapter, q adapter.Query) []string {
	t.Helper()
	rows, err := a.FindMany(ctx, "items", q)
	must(t, err)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.String("id")
	}
	if q.OrderBy == "" {
		slices.Sort(out)
	}
	return out
}

func testOperators(t *testing.T, a adapter.Adapter) {
	seed(t, a)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		where []adapter.Where
		want  []string
	}{
		{"eq", []adapter.Where{adapter.Eq("count", 2)}, []string{"b"}},
		{"eq nil", []adapter.Where{adapter.Eq("count", nil)}, []string{"e"}},
		{"ne", []adapter.Where{adapter.Ne("count", 2)}, []string{"a", "c", "d"}},
		{"ne nil", []adapter.Where{adapter.Ne("count", nil)}, []string{"a", "b", "c", "d"}},
		{"lt", []adapter.Where{adapter.Lt("count", 3)}, []string{"a", "b"}},
		{"lte", []adapter.Where{adapter.Lte("count", 3)}, []string{"a", "b", "c"}},
		{"gt", []adapter.Where{adapter.Gt("count", 3)}, []string{"d"}},
		{"gte", []adapter.Where{adapter.Gte("count", 3)}, []string{"c", "d"}},
		{"time lt", []adapter.Where{adapter.Lt("at", base.Add(90*time.Minute))}, []string{"a", "b"}},
		{"time gte", []adapter.Where{adapter.Gte("at", base.Add(2*time.Hour))}, []string{"c", "d"}},
		{"bool", []adapter.Where{adapter.Eq("done", true)}, []string{"b"}},
		{"in", []adapter.Where{adapter.In("title", "a", "c", "zz")}, []string{"a", "c"}},
		{"in empty", []adapter.Where{adapter.In("title")}, []string{}},
		{"prefix", []adapter.Where{adapter.Prefix("path", "/org/team/")}, []string{"b", "c"}},
		{"prefix case-sensitive", []adapter.Where{adapter.Prefix("path", "/Org")}, []string{"d"}},
		{"prefix no wildcards", []adapter.Where{adapter.Prefix("path", "/%")}, []string{}},
		{"and", []adapter.Where{adapter.Gt("count", 1), adapter.Prefix("path", "/org/")}, []string{"b", "c"}},
		{"none", nil, []string{"a", "b", "c", "d", "e"}},
	}
	for _, c := range cases {
		got := ids(t, a, adapter.Query{Where: c.where})
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		n, err := a.Count(ctx, "items", c.where...)
		must(t, err)
		if int(n) != len(c.want) {
			t.Errorf("%s: count %d, want %d", c.name, n, len(c.want))
		}
	}
	if _, err := a.FindMany(ctx, "items", adapter.Query{Where: []adapter.Where{adapter.Lt("done", true)}}); err == nil {
		t.Error("ordering a bool accepted")
	}
}

func testOrderLimitOffset(t *testing.T, a adapter.Adapter) {
	seed(t, a)
	cases := []struct {
		q    adapter.Query
		want []string
	}{
		{adapter.Query{OrderBy: "count"}, []string{"e", "a", "b", "c", "d"}},
		{adapter.Query{OrderBy: "count", Desc: true}, []string{"d", "c", "b", "a", "e"}},
		{adapter.Query{OrderBy: "at", Limit: 2}, []string{"e", "a"}},
		{adapter.Query{OrderBy: "title", Offset: 3}, []string{"d", "e"}},
		{adapter.Query{OrderBy: "title", Limit: 2, Offset: 1}, []string{"b", "c"}},
		{adapter.Query{OrderBy: "title", Offset: 10}, []string{}},
	}
	for _, c := range cases {
		got := ids(t, a, c.q)
		if !slices.Equal(got, c.want) {
			t.Errorf("%+v: got %v, want %v", c.q, got, c.want)
		}
	}
}

func testUpdate(t *testing.T, a adapter.Adapter) {
	seed(t, a)
	n, err := a.Update(ctx, "items", adapter.Row{"done": true, "path": nil}, adapter.Lt("count", 3))
	must(t, err)
	if n != 2 {
		t.Fatalf("updated %d, want 2", n)
	}
	if got := ids(t, a, adapter.Query{Where: []adapter.Where{adapter.Eq("done", true), adapter.Eq("path", nil)}}); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
	n, err = a.Update(ctx, "items", adapter.Row{"done": true}, adapter.Eq("id", "nope"))
	must(t, err)
	if n != 0 {
		t.Fatalf("updated %d, want 0", n)
	}
}

func testUpdateConflict(t *testing.T, a adapter.Adapter) {
	must(t, a.Create(ctx, "items", adapter.Row{"id": "a", "title": "a", "code": "A"}))
	must(t, a.Create(ctx, "items", adapter.Row{"id": "b", "title": "b", "code": "B"}))
	_, err := a.Update(ctx, "items", adapter.Row{"code": "A"}, adapter.Eq("id", "b"))
	if !errors.Is(err, adapter.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	got, err := a.FindOne(ctx, "items", adapter.Eq("id", "b"))
	must(t, err)
	if got.String("code") != "B" {
		t.Fatalf("code = %q after failed update", got.String("code"))
	}
}

func testIncr(t *testing.T, a adapter.Adapter) {
	seed(t, a)
	_, err := a.Update(ctx, "items", adapter.Row{"count": adapter.Incr(5)}, adapter.In("id", "a", "e"))
	must(t, err)
	for id, want := range map[string]int64{"a": 6, "e": 5} {
		r, err := a.FindOne(ctx, "items", adapter.Eq("id", id))
		must(t, err)
		if r.Int("count") != want {
			t.Errorf("%s: count %d, want %d", id, r.Int("count"), want)
		}
	}
	if _, err := a.Update(ctx, "items", adapter.Row{"title": adapter.Incr(1)}); err == nil {
		t.Error("Incr on a string accepted")
	}
}

func testDelete(t *testing.T, a adapter.Adapter) {
	seed(t, a)
	n, err := a.Delete(ctx, "items", adapter.Prefix("path", "/org/"))
	must(t, err)
	if n != 3 {
		t.Fatalf("deleted %d, want 3", n)
	}
	if got := ids(t, a, adapter.Query{}); !slices.Equal(got, []string{"d", "e"}) {
		t.Fatalf("left %v", got)
	}
}

func testTxCommit(t *testing.T, a adapter.Adapter) {
	err := a.Tx(ctx, func(tx adapter.Adapter) error {
		if err := tx.Create(ctx, "parents", adapter.Row{"id": "p", "name": "p"}); err != nil {
			return err
		}
		// Reads in the transaction see its writes; nested Tx joins it.
		return tx.Tx(ctx, func(tx adapter.Adapter) error {
			if _, err := tx.FindOne(ctx, "parents", adapter.Eq("id", "p")); err != nil {
				return err
			}
			return tx.Create(ctx, "items", adapter.Row{"id": "i", "title": "t", "parent_id": "p"})
		})
	})
	must(t, err)
	if n, err := a.Count(ctx, "items"); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
}

func testTxRollback(t *testing.T, a adapter.Adapter) {
	parent(t, a, "keep")
	boom := errors.New("boom")
	err := a.Tx(ctx, func(tx adapter.Adapter) error {
		if err := tx.Create(ctx, "parents", adapter.Row{"id": "p", "name": "p"}); err != nil {
			return err
		}
		if _, err := tx.Delete(ctx, "parents", adapter.Eq("id", "keep")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the error from fn", err)
	}
	rows, err := a.FindMany(ctx, "parents", adapter.Query{})
	must(t, err)
	if len(rows) != 1 || rows[0].String("id") != "keep" {
		t.Fatalf("after rollback: %v", rows)
	}
	// A conflict inside a transaction rolls back everything.
	err = a.Tx(ctx, func(tx adapter.Adapter) error {
		if err := tx.Create(ctx, "parents", adapter.Row{"id": "q", "name": "q"}); err != nil {
			return err
		}
		return tx.Create(ctx, "parents", adapter.Row{"id": "r", "name": "q"})
	})
	if !errors.Is(err, adapter.ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if n, _ := a.Count(ctx, "parents"); n != 1 {
		t.Fatalf("%d parents after rolled back conflict, want 1", n)
	}
}

func testConcurrentIncr(t *testing.T, a adapter.Adapter) {
	must(t, a.Create(ctx, "items", adapter.Row{"id": "x", "title": "t", "count": 0}))
	const workers, each = 8, 10
	var wg sync.WaitGroup
	errs := make(chan error, workers*each)
	for range workers {
		wg.Go(func() {
			for range each {
				if _, err := a.Update(ctx, "items", adapter.Row{"count": adapter.Incr(1)}, adapter.Eq("id", "x")); err != nil {
					errs <- err
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	r, err := a.FindOne(ctx, "items", adapter.Eq("id", "x"))
	must(t, err)
	if r.Int("count") != workers*each {
		t.Fatalf("count = %d, want %d: increments were lost", r.Int("count"), workers*each)
	}
}

func testMigrateTwice(t *testing.T, a adapter.Adapter) {
	must(t, a.Create(ctx, "parents", adapter.Row{"id": "p", "name": "p"}))
	must(t, a.Migrate(ctx))
	if n, err := a.Count(ctx, "parents"); err != nil || n != 1 {
		t.Fatalf("count after second migrate = %d, %v", n, err)
	}
}

// testMigrateAddsFields migrates an older, smaller schema first, then the
// full one on the same database: fields and indexes are added, rows kept.
func testMigrateAddsFields(t *testing.T, newAdapter func(t *testing.T) adapter.Adapter) {
	a := newAdapter(t)
	old := []schema.Table{
		{Name: "parents", Fields: []schema.Field{{Name: "name", Type: schema.String, Required: true, Unique: true}}},
		{Name: "items", Fields: []schema.Field{{Name: "title", Type: schema.String, Required: true}}},
	}
	must(t, a.Schema(old))
	must(t, a.Migrate(ctx))
	must(t, a.Create(ctx, "items", adapter.Row{"id": "old", "title": "from before"}))

	b, ok := reopen(a)
	if !ok {
		t.Skip("adapter cannot be reopened on the same database")
	}
	must(t, b.Schema(Tables))
	must(t, b.Migrate(ctx))
	r, err := b.FindOne(ctx, "items", adapter.Eq("id", "old"))
	must(t, err)
	if r.String("title") != "from before" || r["code"] != nil {
		t.Fatalf("old row = %v", r)
	}
	must(t, b.Create(ctx, "items", adapter.Row{"id": "new", "title": "t", "code": "c"}))
	if err := b.Create(ctx, "items", adapter.Row{"id": "new2", "title": "t", "code": "c"}); !errors.Is(err, adapter.ErrConflict) {
		t.Fatalf("unique index on added field missing: %v", err)
	}

	// A required field cannot be added to a table that exists.
	c, _ := reopen(a)
	bad := slices.Clone(Tables)
	bad[0] = schema.Table{Name: "parents", Fields: append(slices.Clone(Tables[0].Fields), schema.Field{Name: "must", Type: schema.String, Required: true})}
	must(t, c.Schema(bad))
	if err := c.Migrate(ctx); err == nil {
		t.Fatal("adding a required field to an existing table succeeded")
	}
}

// Reopener is implemented by adapters that can make a fresh adapter (no
// schema yet) over the same database, for the migration test.
type Reopener interface {
	Reopen() adapter.Adapter
}

func reopen(a adapter.Adapter) (adapter.Adapter, bool) {
	r, ok := a.(Reopener)
	if !ok {
		return nil, false
	}
	return r.Reopen(), true
}

func rowsEqual(a, b adapter.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w, ok := b[k]
		if !ok {
			return false
		}
		if tv, ok := v.(time.Time); ok {
			tw, ok := w.(time.Time)
			if !ok || !tv.Equal(tw) {
				return false
			}
			continue
		}
		if fmt.Sprint(v) != fmt.Sprint(w) || fmt.Sprintf("%T", v) != fmt.Sprintf("%T", w) {
			return false
		}
	}
	return true
}
