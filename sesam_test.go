package sesam_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtmn/ablage"
	"github.com/danielgtmn/ablage/memstore"
	"github.com/danielgtmn/sesam/v2"
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

func newAuth(t *testing.T, mod func(*sesam.Config)) (*sesam.Auth, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	cfg := sesam.Config{Store: memstore.New(), BaseURL: "https://app.example.com", Now: c.Now}
	if mod != nil {
		mod(&cfg)
	}
	a, err := sesam.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return a, c
}

func must[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

func TestNewRejects(t *testing.T) {
	cases := map[string]sesam.Config{
		"no store":    {BaseURL: "https://a.example"},
		"no base url": {Store: memstore.New()},
		"bad scheme":  {Store: memstore.New(), BaseURL: "ftp://a.example"},
		"bad origin":  {Store: memstore.New(), BaseURL: "https://a.example", TrustedOrigins: []string{"nope"}},
		"dup plugin":  {Store: memstore.New(), BaseURL: "https://a.example", Plugins: []sesam.Plugin{fakePlugin{}, fakePlugin{}}},
	}
	for name, cfg := range cases {
		if _, err := sesam.New(cfg); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

type fakePlugin struct{}

func (fakePlugin) ID() string             { return "fake" }
func (fakePlugin) Init(*sesam.Auth) error { return nil }

// note is a row of a plugin's table that references users.
type note struct {
	ID     string
	UserID string `db:",ref=auth_users,cascade"`
}

type notesPlugin struct{ notes *ablage.Repository[note] }

func (*notesPlugin) ID() string { return "notes" }
func (p *notesPlugin) Init(a *sesam.Auth) (err error) {
	p.notes, err = ablage.NewRepository[note](a.Store(), "auth_notes")
	return err
}

// A plugin declares its tables in Init; Migrate creates them, and rows that
// reference a user go with it.
func TestPluginTables(t *testing.T) {
	p := &notesPlugin{}
	a, _ := newAuth(t, func(c *sesam.Config) { c.Plugins = []sesam.Plugin{p} })
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}))
	if err := p.notes.Create(ctx, &note{UserID: u.ID}); err != nil {
		t.Fatal(err)
	}
	must[string](t)(a.CreateVerification(ctx, "test", u.ID, time.Hour))
	if err := a.DeleteUser(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := p.notes.Count(ctx, ablage.All); n != 0 {
		t.Fatalf("%d notes left", n)
	}
	if n, _ := a.Store().Count(ctx, sesam.TableVerifications, ablage.All); n != 0 {
		t.Fatalf("%d one-time tokens left", n)
	}
	if err := a.DeleteUser(ctx, u.ID); !errors.Is(err, sesam.ErrNotFound) {
		t.Fatalf("deleting again: %v", err)
	}
}

func TestCreateUser(t *testing.T) {
	a, _ := newAuth(t, nil)
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "  Anna@Example.COM ", Name: " Anna ", EmailVerified: true}))
	if u.Email != "anna@example.com" || u.Name != "Anna" || !u.EmailVerified || u.ID == "" {
		t.Fatalf("user = %+v", u)
	}
	got := must[sesam.User](t)(a.UserByEmail(ctx, "ANNA@example.com"))
	if got.ID != u.ID || !got.CreatedAt.Equal(u.CreatedAt) {
		t.Fatalf("by email = %+v", got)
	}
	if _, err := a.CreateUser(ctx, sesam.NewUser{Email: "anna@example.com"}); !errors.Is(err, sesam.ErrEmailTaken) {
		t.Fatalf("duplicate e-mail: %v", err)
	}
	must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Username: "Ben"}))
	if _, err := a.CreateUser(ctx, sesam.NewUser{Username: "ben", Email: "ben@example.com"}); !errors.Is(err, sesam.ErrUsernameTaken) {
		t.Fatalf("duplicate username: %v", err)
	}
	for name, nu := range map[string]sesam.NewUser{
		"nothing":     {Name: "x"},
		"bad email":   {Email: "Anna <anna@example.com>"},
		"no domain":   {Email: "anna@localhost"},
		"bad name":    {Username: "a b"},
		"short name":  {Username: "ab"},
		"control chr": {Email: "c@example.com", Name: "a\nb"},
	} {
		if _, err := a.CreateUser(ctx, nu); !errors.Is(err, sesam.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want invalid input", name, err)
		}
	}
	if n := must[int64](t)(a.CountUsers(ctx)); n != 2 {
		t.Fatalf("%d users", n)
	}
}

func TestCreateUserHooks(t *testing.T) {
	var after []string
	a, _ := newAuth(t, func(c *sesam.Config) {
		c.Hooks = sesam.Hooks{
			BeforeCreateUser: func(_ context.Context, u *sesam.NewUser) error {
				if strings.HasSuffix(u.Email, "@blocked.example") {
					return sesam.InvalidInput("Not here.")
				}
				u.Name = "set by hook"
				return nil
			},
			AfterCreateUser: func(_ context.Context, u sesam.User) { after = append(after, u.Email) },
		}
	})
	if _, err := a.CreateUser(ctx, sesam.NewUser{Email: "x@blocked.example"}); err == nil || err.Error() != "Not here." {
		t.Fatalf("err = %v", err)
	}
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "ok@example.com"}))
	if u.Name != "set by hook" || len(after) != 1 || after[0] != "ok@example.com" {
		t.Fatalf("user %+v, after %v", u, after)
	}
}

func TestUpdateUser(t *testing.T) {
	a, c := newAuth(t, nil)
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "a@example.com", EmailVerified: true}))
	must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "b@example.com"}))
	c.Add(time.Hour)
	email, name := "A2@example.com", "Anna"
	got := must[sesam.User](t)(a.UpdateUser(ctx, u.ID, sesam.UserUpdate{Email: &email, Name: &name}))
	if got.Email != "a2@example.com" || got.EmailVerified || got.Name != "Anna" || !got.UpdatedAt.After(u.UpdatedAt) {
		t.Fatalf("updated = %+v", got)
	}
	taken := "b@example.com"
	if _, err := a.UpdateUser(ctx, u.ID, sesam.UserUpdate{Email: &taken}); !errors.Is(err, sesam.ErrEmailTaken) {
		t.Fatalf("taken: %v", err)
	}
	empty := ""
	if _, err := a.UpdateUser(ctx, u.ID, sesam.UserUpdate{Email: &empty}); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("removing the only identifier: %v", err)
	}
	if _, err := a.UpdateUser(ctx, "nope", sesam.UserUpdate{Name: &name}); !errors.Is(err, sesam.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestSessionLifetime(t *testing.T) {
	a, c := newAuth(t, func(c *sesam.Config) {
		c.Session = sesam.SessionConfig{IdleTimeout: 2 * time.Hour, MaxLifetime: 5 * time.Hour}
	})
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}))
	token, s, err := a.CreateSession(ctx, u.ID, sesam.SessionMeta{IP: "192.0.2.1", UserAgent: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != sesam.HashToken(token) || s.ID == token {
		t.Fatal("session id is not the token's hash")
	}
	// Used every 90 minutes, the session lives until the maximum lifetime.
	for i := range 3 {
		c.Add(90 * time.Minute)
		if _, _, err := a.Session(ctx, token); err != nil {
			t.Fatalf("use %d: %v", i, err)
		}
	}
	c.Add(31 * time.Minute) // 5h01m after sign-in
	if _, _, err := a.Session(ctx, token); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatalf("after max lifetime: %v", err)
	}

	// Unused, it ends after the idle timeout.
	token, _, _ = a.CreateSession(ctx, u.ID, sesam.SessionMeta{})
	c.Add(2*time.Hour + time.Second)
	if _, _, err := a.Session(ctx, token); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatalf("after idle timeout: %v", err)
	}
	for _, bad := range []string{"", "nope", strings.Repeat("x", 1000)} {
		if _, _, err := a.Session(ctx, bad); !errors.Is(err, sesam.ErrUnauthenticated) {
			t.Errorf("token %.10q: %v", bad, err)
		}
	}
	if err := a.DeleteExpired(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := a.Store().Count(ctx, sesam.TableSessions, ablage.All); n != 0 {
		t.Fatalf("%d sessions left after DeleteExpired", n)
	}
}

func TestRevokeSessions(t *testing.T) {
	var signedOut []string
	a, _ := newAuth(t, func(c *sesam.Config) {
		c.Hooks.AfterSignOut = func(_ context.Context, s sesam.Session) { signedOut = append(signedOut, s.ID) }
	})
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}))
	other := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "b@example.com"}))
	t1, s1, _ := a.CreateSession(ctx, u.ID, sesam.SessionMeta{})
	t2, s2, _ := a.CreateSession(ctx, u.ID, sesam.SessionMeta{})
	t3, _, _ := a.CreateSession(ctx, u.ID, sesam.SessionMeta{})
	to, so, _ := a.CreateSession(ctx, other.ID, sesam.SessionMeta{})

	if list := must[[]sesam.Session](t)(a.Sessions(ctx, u.ID)); len(list) != 3 {
		t.Fatalf("%d sessions", len(list))
	}
	if err := a.RevokeSession(ctx, u.ID, so.ID); !errors.Is(err, sesam.ErrNotFound) {
		t.Fatalf("revoking another user's session: %v", err)
	}
	if err := a.RevokeSession(ctx, u.ID, s2.ID); err != nil {
		t.Fatal(err)
	}
	if n := must[int64](t)(a.RevokeUserSessions(ctx, u.ID, s1.ID)); n != 1 {
		t.Fatalf("revoked %d, want 1 (t3)", n)
	}
	for tok, alive := range map[string]bool{t1: true, t2: false, t3: false, to: true} {
		_, _, err := a.Session(ctx, tok)
		if (err == nil) != alive {
			t.Errorf("session alive = %v, want %v", err == nil, alive)
		}
	}
	if err := a.SignOut(ctx, t1); err != nil {
		t.Fatal(err)
	}
	if err := a.SignOut(ctx, t1); err != nil {
		t.Fatal("signing out twice:", err)
	}
	if len(signedOut) != 1 || signedOut[0] != s1.ID {
		t.Fatalf("sign-out hook got %v", signedOut)
	}
	if err := a.DeleteUser(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Session(ctx, to); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatalf("session of deleted user: %v", err)
	}
}

func TestLimit(t *testing.T) {
	a, c := newAuth(t, nil)
	for i := range 3 {
		if err := a.Limit(ctx, "k", 3, 10*time.Minute); err != nil {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	c.Add(4 * time.Minute)
	err := a.Limit(ctx, "k", 3, 10*time.Minute)
	var rl *sesam.RateLimitError
	if !errors.As(err, &rl) || !errors.Is(err, sesam.ErrRateLimited) {
		t.Fatalf("4th attempt: %v", err)
	}
	if rl.RetryAfter != 6*time.Minute {
		t.Fatalf("retry after %v, want 6m", rl.RetryAfter)
	}
	if rl.Error() != "Too many attempts. Try again in 6 minutes." {
		t.Fatalf("message %q", rl.Error())
	}
	// Other keys are separate.
	if err := a.Limit(ctx, "other", 3, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	c.Add(6 * time.Minute)
	if err := a.Limit(ctx, "k", 3, 10*time.Minute); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	for range 3 {
		_ = a.Limit(ctx, "k", 3, 10*time.Minute)
	}
	if err := a.ResetLimit(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if err := a.Limit(ctx, "k", 3, 10*time.Minute); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	if err := a.Limit(ctx, "k", 0, time.Minute); err == nil {
		t.Fatal("max 0 accepted")
	}
}

func TestVerification(t *testing.T) {
	a, c := newAuth(t, nil)
	tok := must[string](t)(a.CreateVerification(ctx, "reset", "user1", time.Hour))
	if _, err := a.ConsumeVerification(ctx, "other", tok); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("wrong purpose: %v", err)
	}
	if id := must[string](t)(a.ConsumeVerification(ctx, "reset", tok)); id != "user1" {
		t.Fatalf("identifier %q", id)
	}
	if _, err := a.ConsumeVerification(ctx, "reset", tok); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("second use: %v", err)
	}
	// A new token replaces the old one.
	old := must[string](t)(a.CreateVerification(ctx, "reset", "user1", time.Hour))
	cur := must[string](t)(a.CreateVerification(ctx, "reset", "user1", time.Hour))
	if _, err := a.ConsumeVerification(ctx, "reset", old); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("replaced token: %v", err)
	}
	c.Add(time.Hour)
	if _, err := a.ConsumeVerification(ctx, "reset", cur); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("expired token: %v", err)
	}
	// Used at the same time, a token works exactly once.
	tok = must[string](t)(a.CreateVerification(ctx, "reset", "user2", time.Hour))
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for range 10 {
		wg.Go(func() {
			if _, err := a.ConsumeVerification(ctx, "reset", tok); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("token used %d times", ok)
	}
}

func TestCookieAndHandler(t *testing.T) {
	a, _ := newAuth(t, nil)
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}))
	token, _, _ := a.CreateSession(ctx, u.ID, sesam.SessionMeta{})

	rec := httptest.NewRecorder()
	a.SetSessionCookie(rec, token)
	ck := rec.Result().Cookies()[0]
	if ck.Name != "__Host-sesam_session" || !ck.HttpOnly || !ck.Secure || ck.Path != "/" || ck.Domain != "" ||
		ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 30*24*3600 {
		t.Fatalf("cookie = %+v", ck)
	}

	h := a.Handler()
	req := httptest.NewRequest("GET", "/api/auth/session", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), `"code":"unauthenticated"`) {
		t.Fatalf("no cookie: %d %s", rec.Code, rec.Body)
	}
	req.AddCookie(&http.Cookie{Name: "__Host-sesam_session", Value: token})
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), u.ID) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("with cookie: %d %s", rec.Code, rec.Body)
	}

	// Sign-out from another site is refused, from the app it works.
	out := httptest.NewRequest("POST", "/api/auth/sign-out", nil)
	out.AddCookie(&http.Cookie{Name: "__Host-sesam_session", Value: token})
	out.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, out)
	if rec.Code != 403 {
		t.Fatalf("cross-site sign-out: %d", rec.Code)
	}
	out.Header.Set("Origin", "https://app.example.com")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, out)
	if rec.Code != 204 || rec.Result().Cookies()[0].MaxAge != -1 {
		t.Fatalf("sign-out: %d %v", rec.Code, rec.Result().Cookies())
	}
	if _, _, err := a.Session(ctx, token); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatal("session still valid after sign-out")
	}
}

func TestCheckRequest(t *testing.T) {
	a, _ := newAuth(t, func(c *sesam.Config) {
		c.TrustedOrigins = []string{"http://localhost:5173"}
		c.RequireHeader = "X-App-Request"
	})
	cases := []struct {
		method, origin, site, header string
		ok                           bool
	}{
		{"GET", "https://evil.example", "", "", true},
		{"POST", "https://app.example.com", "", "1", true},
		{"POST", "HTTPS://APP.example.com", "", "1", true},
		{"POST", "http://localhost:5173", "", "1", true},
		{"POST", "", "", "1", true}, // not from a browser
		{"POST", "https://app.example.com", "", "", false},
		{"POST", "https://evil.example", "", "1", false},
		{"POST", "http://app.example.com", "", "1", false},
		{"POST", "null", "", "1", false},
		{"DELETE", "", "cross-site", "1", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "/x", nil)
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.site != "" {
			r.Header.Set("Sec-Fetch-Site", c.site)
		}
		if c.header != "" {
			r.Header.Set("X-App-Request", c.header)
		}
		if err := a.CheckRequest(r); (err == nil) != c.ok {
			t.Errorf("%+v: err = %v", c, err)
		}
	}
}

func TestMiddleware(t *testing.T) {
	a, _ := newAuth(t, func(c *sesam.Config) { c.BaseURL = "http://localhost:8080" })
	u := must[sesam.User](t)(a.CreateUser(ctx, sesam.NewUser{Username: "anna"}))
	token, _, _ := a.CreateSession(ctx, u.ID, sesam.SessionMeta{})
	if a.CookieName() != "sesam_session" {
		t.Fatalf("cookie name over http = %q", a.CookieName())
	}
	app := a.Middleware(a.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur, _ := sesam.FromContext(r.Context())
		w.Write([]byte(cur.User.Username))
	})))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatalf("without session: %d", rec.Code)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: "sesam_session", Value: token})
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "anna" {
		t.Fatalf("with session: %d %q", rec.Code, rec.Body)
	}
	req = httptest.NewRequest("POST", "/", nil)
	req.Header.Set("Origin", "https://evil.example")
	req.AddCookie(&http.Cookie{Name: "sesam_session", Value: token})
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-site POST with cookie: %d", rec.Code)
	}
}

func TestSendEmail(t *testing.T) {
	a, _ := newAuth(t, nil)
	if a.CanSendEmail() {
		t.Fatal("CanSendEmail without SendEmail")
	}
	if err := a.SendEmail(ctx, sesam.Email{To: "a@example.com", Subject: "s", Text: "t"}); !errors.Is(err, sesam.ErrEmailUnavailable) {
		t.Fatalf("without SendEmail: %v", err)
	}
	var got []sesam.Email
	a, _ = newAuth(t, func(c *sesam.Config) {
		c.SendEmail = func(_ context.Context, e sesam.Email) error { got = append(got, e); return nil }
	})
	if !a.CanSendEmail() {
		t.Fatal("CanSendEmail false")
	}
	if err := a.SendEmail(ctx, sesam.Email{To: "a@example.com", Subject: "s"}); err == nil {
		t.Fatal("e-mail without text accepted")
	}
	if err := a.SendEmail(ctx, sesam.Email{To: "a@example.com", Subject: "s", Text: "t"}); err != nil || len(got) != 1 {
		t.Fatalf("send: %v, %d", err, len(got))
	}
}
