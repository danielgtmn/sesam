package sqladapter_test

import (
	"crypto/rand"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/adapters/adaptertest"
	"github.com/danielgtmn/sesam/adapters/sqladapter"
	"github.com/danielgtmn/sesam/schema"
)

// reopenable lets adaptertest open a second adapter on the same database.
type reopenable struct {
	*sqladapter.Adapter
	db *sql.DB
	d  sqladapter.Dialect
}

func (r reopenable) Reopen() adapter.Adapter { return wrap(nil, r.db, r.d) }

func wrap(t *testing.T, db *sql.DB, d sqladapter.Dialect) adapter.Adapter {
	a, err := sqladapter.New(db, d)
	if err != nil {
		t.Fatal(err)
	}
	return reopenable{a, db, d}
}

// OpenSQLite opens an empty SQLite database the way apps are told to.
func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	file := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", file+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestSQLite(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) adapter.Adapter {
		return wrap(t, openSQLite(t), sqladapter.SQLite)
	})
}

// One connection, as gpanel runs SQLite.
func TestSQLiteSingleConnection(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) adapter.Adapter {
		db := openSQLite(t)
		db.SetMaxOpenConns(1)
		return wrap(t, db, sqladapter.SQLite)
	})
}

// TestPostgres needs SESAM_TEST_POSTGRES, e.g.
// postgres://postgres@localhost:5432/postgres?sslmode=disable.
// Every test gets its own schema, dropped afterwards.
func TestPostgres(t *testing.T) {
	adaptertest.Run(t, func(t *testing.T) adapter.Adapter {
		return wrap(t, openPostgres(t), sqladapter.Postgres)
	})
}

func openPostgres(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("SESAM_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("SESAM_TEST_POSTGRES not set")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := "sesam_test_" + strings.ToLower(rand.Text()[:10])
	if _, err := admin.Exec("CREATE SCHEMA " + name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP SCHEMA " + name + " CASCADE") })
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestDDL(t *testing.T) {
	tables, err := schema.Merge(adaptertest.Tables...)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []sqladapter.Dialect{sqladapter.SQLite, sqladapter.Postgres} {
		a, err := sqladapter.New(&sql.DB{}, d, sqladapter.Options{TablePrefix: "x_"})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Schema(tables); err != nil {
			t.Fatal(err)
		}
		stmts, err := a.DDL()
		if err != nil {
			t.Fatal(err)
		}
		all := strings.Join(stmts, ";\n")
		for _, want := range []string{
			`CREATE TABLE IF NOT EXISTS "x_items"`,
			`"parent_id" TEXT REFERENCES "x_parents"("id")`,
			`CREATE UNIQUE INDEX IF NOT EXISTS "x_items_parent_id_title_key" ON "x_items" ("parent_id", "title")`,
			`CREATE INDEX IF NOT EXISTS "x_items_path_idx"`,
		} {
			if !strings.Contains(all, want) {
				t.Errorf("%s: DDL lacks %s:\n%s", d, want, all)
			}
		}
	}
}

func TestNewRejects(t *testing.T) {
	if _, err := sqladapter.New(nil, sqladapter.SQLite); err == nil {
		t.Error("nil db accepted")
	}
	if _, err := sqladapter.New(&sql.DB{}, sqladapter.Dialect{}); err == nil {
		t.Error("no dialect accepted")
	}
	if _, err := sqladapter.New(&sql.DB{}, sqladapter.SQLite, sqladapter.Options{TablePrefix: `a"; DROP`}); err == nil {
		t.Error("bad prefix accepted")
	}
}
