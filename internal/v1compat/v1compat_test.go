package v1compat

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	v1 "github.com/danielgtmn/sesam"
	"github.com/danielgtmn/sesam/adapters/sqladapter"
	v1password "github.com/danielgtmn/sesam/plugins/password"

	"github.com/danielgtmn/ablage"
	"github.com/danielgtmn/ablage/sqlstore"

	"github.com/danielgtmn/sesam/v2"
	"github.com/danielgtmn/sesam/v2/plugins/password"
)

var ctx = context.Background()

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type outbox struct {
	mu   sync.Mutex
	last string
}

func (o *outbox) send(_ context.Context, text string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.last = text
}

func (o *outbox) token() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	i := strings.Index(o.last, "token=")
	if i < 0 {
		return ""
	}
	return strings.Fields(o.last[i+len("token="):])[0]
}

func resetURL(t string) string { return "https://app.example.com/reset?token=" + t }

const (
	pass    = "a long password"
	newPass = "another long password"
)

var meta = v1.SessionMeta{IP: "192.0.2.7", UserAgent: "test"}

// state is what sesam v1 left behind.
type state struct {
	anna, ben  v1.User
	token      string // anna's session
	resetToken string // a reset link for ben
}

// TestV1Database creates a database with sesam v1.0.0, then opens it with
// this version and goes on: sessions, passwords, attempt counters and
// reset links carry over, uniqueness still holds, and Migrate leaves one
// index per purpose.
func TestV1Database(t *testing.T) {
	for _, d := range []struct {
		name    string
		open    func(*testing.T) *sql.DB
		v1      sqladapter.Dialect
		dialect sqlstore.Dialect
	}{
		{"SQLite", openSQLite, sqladapter.SQLite, sqlstore.SQLite},
		{"Postgres", openPostgres, sqladapter.Postgres, sqlstore.Postgres},
	} {
		t.Run(d.name, func(t *testing.T) {
			db := d.open(t)
			c := &clock{t: time.Date(2026, 11, 20, 9, 0, 0, 123456789, time.UTC)}
			box := &outbox{}
			st := createV1(t, db, d.v1, c, box)
			before := indexes(t, db, d.dialect)

			c.Add(time.Minute)
			store, err := sqlstore.New(db, d.dialect)
			if err != nil {
				t.Fatal(err)
			}
			pw := password.New(password.Options{Identifier: password.EmailOrUsername, MaxAttemptsPerName: 3, ResetURL: resetURL})
			a, err := sesam.New(sesam.Config{
				Store: store, BaseURL: "https://app.example.com", Now: c.Now, Plugins: []sesam.Plugin{pw},
				SendEmail: func(ctx context.Context, e sesam.Email) error { box.send(ctx, e.Text); return nil },
			})
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := a.Migrate(ctx); err != nil {
					t.Fatal(err)
				}
			}
			checkIndexes(t, before, indexes(t, db, d.dialect))

			// Users read as v1 wrote them, NULLs included.
			anna, err := a.User(ctx, st.anna.ID)
			if err != nil {
				t.Fatal(err)
			}
			if anna.Email != "anna@example.com" || anna.Username != "anna" || anna.Name != "Anna" || anna.EmailVerified ||
				!anna.CreatedAt.Equal(st.anna.CreatedAt) || !anna.UpdatedAt.Equal(st.anna.UpdatedAt) {
				t.Errorf("anna = %+v, v1 had %+v", anna, st.anna)
			}
			if ben, err := a.UserByUsername(ctx, "ben"); err != nil || ben.Email != "" || ben.ID != st.ben.ID {
				t.Errorf("ben = %+v, %v", ben, err)
			}
			list, err := a.Users(ctx, sesam.ListOptions{})
			if err != nil || len(list) != 2 || list[0].ID != st.anna.ID {
				t.Errorf("users = %+v, %v", list, err)
			}

			// The session from v1 is still signed in and is extended.
			s, u, err := a.Session(ctx, st.token)
			if err != nil || u.ID != st.anna.ID || s.IP != meta.IP || s.UserAgent != meta.UserAgent {
				t.Fatalf("v1 session: %+v %v", s, err)
			}
			if sessions, _ := a.Sessions(ctx, anna.ID); len(sessions) != 1 || !sessions[0].LastSeenAt.Equal(ablage.NormalizeTime(c.Now())) {
				t.Errorf("sessions = %+v", sessions)
			}

			// Failed attempts counted by v1 keep counting: 2 there, the
			// third here is allowed, the fourth is refused.
			bad := password.Credentials{Identifier: "anna", Password: "wrong password"}
			if _, _, _, err := pw.SignIn(ctx, bad, sesam.SessionMeta(meta)); !errors.Is(err, sesam.ErrInvalidCredentials) {
				t.Fatalf("third attempt: %v", err)
			}
			var rl *sesam.RateLimitError
			if _, _, _, err := pw.SignIn(ctx, bad, sesam.SessionMeta(meta)); !errors.As(err, &rl) {
				t.Fatalf("fourth attempt: %v", err)
			}
			c.Add(16 * time.Minute)
			// The hash v1 stored signs in.
			if _, token, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "Anna@Example.com", Password: pass}, sesam.SessionMeta(meta)); err != nil || token == "" {
				t.Fatalf("sign-in with the v1 password: %v", err)
			}

			// The reset link v1 sent still works, once.
			if u, err := pw.ResetPassword(ctx, st.resetToken, newPass); err != nil || u.ID != st.ben.ID {
				t.Fatalf("v1 reset link: %v", err)
			}
			if _, err := pw.ResetPassword(ctx, st.resetToken, newPass); !errors.Is(err, sesam.ErrInvalidToken) {
				t.Fatalf("v1 reset link twice: %v", err)
			}
			if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "ben", Password: newPass}, sesam.SessionMeta(meta)); err != nil {
				t.Fatalf("sign-in after reset: %v", err)
			}

			// Uniqueness from v1's indexes holds; users without e-mail
			// address do not collide.
			if _, err := pw.CreateUser(ctx, sesam.NewUser{Username: "ANNA"}, pass); !errors.Is(err, sesam.ErrUsernameTaken) {
				t.Errorf("taken username: %v", err)
			}
			if _, err := pw.CreateUser(ctx, sesam.NewUser{Email: "anna@example.com", Username: "anna2"}, pass); !errors.Is(err, sesam.ErrEmailTaken) {
				t.Errorf("taken e-mail: %v", err)
			}
			carl, err := pw.CreateUser(ctx, sesam.NewUser{Username: "carl"}, pass)
			if err != nil {
				t.Fatalf("second user without e-mail: %v", err)
			}
			if n, _ := a.CountUsers(ctx); n != 3 {
				t.Errorf("%d users", n)
			}

			// Deleting a user removes what v1 stored for it.
			if err := a.DeleteUser(ctx, st.anna.ID); err != nil {
				t.Fatal(err)
			}
			if _, _, err := a.Session(ctx, st.token); !errors.Is(err, sesam.ErrUnauthenticated) {
				t.Errorf("session of a deleted user: %v", err)
			}
			if n, _ := a.Accounts().Count(ctx, ablage.Eq("user_id", st.anna.ID)); n != 0 {
				t.Errorf("%d accounts of a deleted user", n)
			}
			if err := a.DeleteUser(ctx, carl.ID); err != nil {
				t.Fatal(err)
			}
			c.Add(8 * 24 * time.Hour)
			if err := a.DeleteExpired(ctx); err != nil {
				t.Fatal(err)
			}
			if n, _ := store.Count(ctx, sesam.TableSessions, ablage.All); n != 0 {
				t.Errorf("%d sessions after they expired", n)
			}
		})
	}
}

func createV1(t *testing.T, db *sql.DB, d sqladapter.Dialect, c *clock, box *outbox) state {
	t.Helper()
	ad, err := sqladapter.New(db, d)
	if err != nil {
		t.Fatal(err)
	}
	pw := v1password.New(v1password.Options{Identifier: v1password.EmailOrUsername, MaxAttemptsPerName: 3, ResetURL: resetURL})
	a, err := v1.New(v1.Config{
		Adapter: ad, BaseURL: "https://app.example.com", Now: c.Now, Plugins: []v1.Plugin{pw},
		SendEmail: func(ctx context.Context, e v1.Email) error { box.send(ctx, e.Text); return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var st state
	if st.anna, err = pw.CreateUser(ctx, v1.NewUser{Email: "anna@example.com", Username: "anna", Name: "Anna"}, pass); err != nil {
		t.Fatal(err)
	}
	c.Add(time.Second)
	if st.ben, err = pw.CreateUser(ctx, v1.NewUser{Username: "ben", Email: "ben@example.com"}, pass); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateUser(ctx, st.ben.ID, v1.UserUpdate{Email: new(string)}); err != nil {
		t.Fatal(err)
	}
	if _, st.token, _, err = pw.SignIn(ctx, v1password.Credentials{Identifier: "anna", Password: pass}, meta); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, _, _, err := pw.SignIn(ctx, v1password.Credentials{Identifier: "anna", Password: "wrong password"}, meta); !errors.Is(err, v1.ErrInvalidCredentials) {
			t.Fatal(err)
		}
	}
	// A reset link for ben, sent while ben still had an address.
	if err := a.Limit(ctx, "unused", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := a.UpdateUser(ctx, st.ben.ID, v1.UserUpdate{Email: ptr("ben@example.com")}); err != nil {
		t.Fatal(err)
	}
	if err := pw.RequestReset(ctx, "ben@example.com", meta); err != nil {
		t.Fatal(err)
	}
	if st.resetToken = box.token(); st.resetToken == "" {
		t.Fatal("no reset link sent")
	}
	if _, err := a.UpdateUser(ctx, st.ben.ID, v1.UserUpdate{Email: new(string)}); err != nil {
		t.Fatal(err)
	}
	if st.anna, err = a.User(ctx, st.anna.ID); err != nil {
		t.Fatal(err)
	}
	return st
}

func ptr[T any](v T) *T { return &v }

// indexes lists the indexes of sesam's tables: name -> table and columns.
func indexes(t *testing.T, db *sql.DB, d sqlstore.Dialect) map[string]string {
	t.Helper()
	q := `SELECT il.name, m.name || ':' || group_concat(ii.name, ',')
		FROM sqlite_master m, pragma_index_list(m.name) il, pragma_index_info(il.name) ii
		WHERE m.type = 'table' AND m.name LIKE 'auth\_%' ESCAPE '\' AND il.origin = 'c'
		GROUP BY il.name`
	if d == sqlstore.Postgres {
		q = `SELECT i.relname, t.relname || ':' || string_agg(a.attname, ',' ORDER BY k.ord)
		FROM pg_index x JOIN pg_class i ON i.oid = x.indexrelid JOIN pg_class t ON t.oid = x.indrelid
		JOIN pg_namespace n ON n.oid = t.relnamespace
		CROSS JOIN LATERAL unnest(x.indkey::int2[]) WITH ORDINALITY k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
		WHERE n.nspname = current_schema() AND t.relname LIKE 'auth\_%' AND NOT x.indisprimary
		GROUP BY i.relname, t.relname`
	}
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			t.Fatal(err)
		}
		out[name] = def
	}
	return out
}

// checkIndexes: every index v1 created is still there in purpose (same
// table and columns), and none twice.
func checkIndexes(t *testing.T, before, after map[string]string) {
	t.Helper()
	defs := func(m map[string]string) []string {
		var out []string
		for _, d := range m {
			out = append(out, d)
		}
		slices.Sort(out)
		return out
	}
	b, a := defs(before), defs(after)
	if len(b) < 10 {
		t.Fatalf("v1 created only %d indexes: %v", len(b), before)
	}
	if !slices.Equal(b, a) {
		t.Errorf("indexes changed:\nv1:  %v\nnow: %v", before, after)
	}
}

func openSQLite(t *testing.T) *sql.DB {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// openPostgres opens a new database whose text sorts by language rules,
// as Postgres does on most systems (the Alpine image of the tests sorts by
// bytes otherwise), so the columns v1 created have such a collation.
func openPostgres(t *testing.T) *sql.DB {
	dsn := os.Getenv("SESAM_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("SESAM_TEST_POSTGRES not set")
	}
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := "sesam_v1_" + strings.ToLower(rand.Text()[:10])
	if _, err := admin.Exec("CREATE DATABASE " + name + " TEMPLATE template0 LOCALE_PROVIDER icu ICU_LOCALE 'und' LOCALE 'C'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Exec("DROP DATABASE " + name + " WITH (FORCE)") })
	u, _ := url.Parse(dsn)
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	var sorted string
	if err := db.QueryRow(`SELECT string_agg(x, '' ORDER BY x) FROM (VALUES ('B'), ('a')) v(x)`).Scan(&sorted); err != nil || sorted != "aB" {
		t.Fatalf("database sorts %q, %v; want language rules", sorted, err)
	}
	return db
}
