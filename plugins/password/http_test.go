package password_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"github.com/danielgtmn/sesam"
	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/adapters/memadapter"
	"github.com/danielgtmn/sesam/adapters/sqladapter"
	"github.com/danielgtmn/sesam/plugins/password"
)

// TestHTTPFlow drives the endpoints with a real HTTP client and cookie jar,
// the way a browser app uses them, on every database.
func TestHTTPFlow(t *testing.T) {
	for name, open := range map[string]func(*testing.T) adapter.Adapter{
		"memory":   func(*testing.T) adapter.Adapter { return memadapter.New() },
		"sqlite":   openSQLite,
		"postgres": openPostgres,
	} {
		t.Run(name, func(t *testing.T) { httpFlow(t, open(t)) })
	}
}

func openSQLite(t *testing.T) adapter.Adapter {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "app.db")+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := sqladapter.New(db, sqladapter.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func openPostgres(t *testing.T) adapter.Adapter {
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
	u, _ := url.Parse(dsn)
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	db, err := sql.Open("pgx", u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a, err := sqladapter.New(db, sqladapter.Postgres)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type client struct {
	t      *testing.T
	c      *http.Client
	base   string
	origin string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = strings.NewReader(string(b))
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", c.origin)
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func httpFlow(t *testing.T, db adapter.Adapter) {
	m := &mailer{}
	srv := httptest.NewUnstartedServer(nil)
	srv.Start()
	defer srv.Close()
	pw := password.New(password.Options{
		Mailer:   m,
		ResetURL: func(tok string) string { return srv.URL + "/reset?token=" + tok },
	})
	a, err := sesam.New(sesam.Config{Adapter: db, BaseURL: srv.URL, Plugins: []sesam.Plugin{pw}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", a.Handler())
	mux.Handle("/app", a.Middleware(a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur, _ := sesam.FromContext(r.Context())
		sesam.WriteJSON(w, 200, map[string]string{"hello": cur.User.Email})
	}))))
	srv.Config.Handler = mux

	jar, _ := cookiejar.New(nil)
	c := &client{t: t, c: &http.Client{Jar: jar}, base: srv.URL, origin: srv.URL}

	if code, _ := c.do("GET", "/app", nil); code != 401 {
		t.Fatalf("app before sign-up: %d", code)
	}
	code, body := c.do("POST", "/api/auth/sign-up", map[string]string{"email": "anna@example.com", "name": "Anna", "password": pass})
	if code != 201 || body["user"].(map[string]any)["email"] != "anna@example.com" {
		t.Fatalf("sign-up: %d %v", code, body)
	}
	if code, body := c.do("GET", "/app", nil); code != 200 || body["hello"] != "anna@example.com" {
		t.Fatalf("app after sign-up: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/auth/sign-out", nil); code != 204 {
		t.Fatalf("sign-out: %d", code)
	}
	if code, _ := c.do("GET", "/api/auth/session", nil); code != 401 {
		t.Fatalf("session after sign-out: %d", code)
	}
	if code, body := c.do("POST", "/api/auth/sign-in", map[string]string{"email": "anna@example.com", "password": "wrong password"}); code != 401 || body["code"] != "invalid_credentials" {
		t.Fatalf("wrong password: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/auth/sign-in", map[string]string{"email": "anna@example.com", "password": pass}); code != 200 {
		t.Fatalf("sign-in: %d", code)
	}
	// A second device.
	jar2, _ := cookiejar.New(nil)
	c2 := &client{t: t, c: &http.Client{Jar: jar2}, base: srv.URL, origin: srv.URL}
	c2.do("POST", "/api/auth/sign-in", map[string]string{"email": "anna@example.com", "password": pass})
	if code, body := c.do("GET", "/api/auth/sessions", nil); code != 200 || len(body["sessions"].([]any)) != 2 {
		t.Fatalf("sessions: %d %v", code, body)
	}

	if code, _ := c.do("POST", "/api/auth/change-password", map[string]string{"current": pass, "new": "changed password"}); code != 204 {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := c.do("GET", "/app", nil); code != 200 {
		t.Fatalf("own session after change: %d", code)
	}
	if code, _ := c2.do("GET", "/app", nil); code != 401 {
		t.Fatalf("other device after change: %d", code)
	}

	// A page on another site cannot sign in or change anything.
	evil := &client{t: t, c: &http.Client{Jar: jar}, base: srv.URL, origin: "https://evil.example"}
	if code, _ := evil.do("POST", "/api/auth/change-password", map[string]string{"current": "changed password", "new": "evil password!"}); code != 403 {
		t.Fatalf("cross-site change: %d", code)
	}

	// Forgotten password.
	if code, _ := c2.do("POST", "/api/auth/request-password-reset", map[string]string{"email": "anna@example.com"}); code != 204 {
		t.Fatalf("request reset: %d", code)
	}
	if code, _ := c2.do("POST", "/api/auth/request-password-reset", map[string]string{"email": "nobody@example.com"}); code != 204 {
		t.Fatalf("request reset for unknown address: %d", code)
	}
	tok := m.token("anna@example.com")
	if code, _ := c2.do("POST", "/api/auth/reset-password", map[string]string{"token": tok, "password": "reset password"}); code != 204 {
		t.Fatalf("reset: %d", code)
	}
	if code, _ := c.do("GET", "/app", nil); code != 401 {
		t.Fatalf("session after reset: %d", code)
	}
	if code, _ := c2.do("POST", "/api/auth/sign-in", map[string]string{"email": "anna@example.com", "password": "reset password"}); code != 200 {
		t.Fatalf("sign-in with reset password: %d", code)
	}
	if code, body := c2.do("POST", "/api/auth/sign-in", map[string]string{"email": "x@example.com", "password": pass, "extra": "x"}); code != 400 {
		t.Fatalf("unknown field: %d %v", code, body)
	}
}

// One login field "name or e-mail address" works whichever JSON field the
// page sends it in.
func TestHTTPSignInEitherField(t *testing.T) {
	pw := password.New(password.Options{Identifier: password.EmailOrUsername})
	a, err := sesam.New(sesam.Config{Adapter: memadapter.New(), BaseURL: "http://localhost", Plugins: []sesam.Plugin{pw}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Username: "anna", Email: "anna@example.com"}, pass); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Handler())
	defer srv.Close()
	c := &client{t: t, c: srv.Client(), base: srv.URL, origin: "http://localhost"}
	for _, body := range []map[string]string{
		{"username": "anna", "password": pass},
		{"username": "anna@example.com", "password": pass},
		{"email": "anna@example.com", "password": pass},
		{"email": "ANNA", "password": pass},
	} {
		if code, out := c.do("POST", "/api/auth/sign-in", body); code != 200 {
			t.Errorf("%v: %d %v", body, code, out)
		}
	}
}
