package password_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtmn/sesam"
	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/adapters/memadapter"
	"github.com/danielgtmn/sesam/plugins/password"
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

type mailer struct {
	mu    sync.Mutex
	links map[string]string // e-mail -> last link
}

func (m *mailer) SendPasswordReset(_ context.Context, u sesam.User, link string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.links == nil {
		m.links = map[string]string{}
	}
	m.links[u.Email] = link
	return nil
}

func (m *mailer) token(email string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	link := m.links[email]
	return link[strings.Index(link, "token=")+len("token="):]
}

func setup(t *testing.T, db adapter.Adapter, o password.Options) (*sesam.Auth, *password.Plugin, *clock) {
	t.Helper()
	c := &clock{t: time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)}
	pw := password.New(o)
	a, err := sesam.New(sesam.Config{Adapter: db, BaseURL: "https://app.example.com", Now: c.Now, Plugins: []sesam.Plugin{pw}})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return a, pw, c
}

var meta = sesam.SessionMeta{IP: "192.0.2.7", UserAgent: "test"}

const pass = "a long password"

func TestSignUpAndSignIn(t *testing.T) {
	a, pw, _ := setup(t, memadapter.New(), password.Options{})
	u, token, _, err := pw.SignUp(ctx, password.SignUpInput{Email: "Anna@Example.com", Name: "Anna", Password: pass}, meta)
	if err != nil {
		t.Fatal(err)
	}
	if _, su, err := a.Session(ctx, token); err != nil || su.ID != u.ID {
		t.Fatalf("session after sign-up: %v", err)
	}
	got, token, s, err := pw.SignIn(ctx, password.Credentials{Identifier: " ANNA@example.com", Password: pass}, meta)
	if err != nil || got.ID != u.ID || token == "" || s.IP != meta.IP {
		t.Fatalf("sign-in: %+v %v", got, err)
	}
	if ok, _ := pw.HasPassword(ctx, u.ID); !ok {
		t.Fatal("no password account")
	}
	// The stored hash is argon2id, never the password.
	acc, _ := a.Adapter().FindOne(ctx, sesam.TableAccounts, adapter.Eq("user_id", u.ID))
	if h := acc.String("password_hash"); !strings.HasPrefix(h, "$argon2id$") || strings.Contains(h, pass) {
		t.Fatalf("stored %q", h)
	}

	for name, c := range map[string]password.Credentials{
		"wrong password": {Identifier: "anna@example.com", Password: pass + "!"},
		"unknown user":   {Identifier: "nobody@example.com", Password: pass},
		"not an address": {Identifier: "anna", Password: pass},
		"huge password":  {Identifier: "anna@example.com", Password: strings.Repeat("x", 5000)},
	} {
		if _, _, _, err := pw.SignIn(ctx, c, sesam.SessionMeta{IP: name}); !errors.Is(err, sesam.ErrInvalidCredentials) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, _, err := pw.SignUp(ctx, password.SignUpInput{Email: "anna@example.com", Password: pass}, meta); !errors.Is(err, sesam.ErrEmailTaken) {
		t.Fatalf("second sign-up: %v", err)
	}
	if _, _, _, err := pw.SignUp(ctx, password.SignUpInput{Email: "b@example.com", Password: "short"}, meta); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("short password: %v", err)
	}
	if _, _, _, err := pw.SignUp(ctx, password.SignUpInput{Username: "bob", Password: pass}, meta); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("sign-up without e-mail in e-mail mode: %v", err)
	}
	if n, _ := a.CountUsers(ctx); n != 1 {
		t.Fatalf("%d users; failed sign-ups must not leave users behind", n)
	}
}

func TestUnknownUserTakesAsLongAsWrongPassword(t *testing.T) {
	_, pw, _ := setup(t, memadapter.New(), password.Options{MaxAttemptsPerName: 100, MaxAttemptsPerClient: 100})
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}, pass); err != nil {
		t.Fatal(err)
	}
	measure := func(id string) time.Duration {
		start := time.Now()
		for range 5 {
			pw.SignIn(ctx, password.Credentials{Identifier: id, Password: "wrong password"}, meta)
		}
		return time.Since(start)
	}
	measure("warm@example.com") // creates the dummy hash
	known, unknown := measure("a@example.com"), measure("x@example.com")
	if unknown < known/3 {
		t.Fatalf("unknown user %v, wrong password %v: the difference reveals which users exist", unknown, known)
	}
}

func TestSignInLimits(t *testing.T) {
	_, pw, c := setup(t, memadapter.New(), password.Options{})
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}, pass); err != nil {
		t.Fatal(err)
	}
	wrong := password.Credentials{Identifier: "a@example.com", Password: "wrong password"}
	for i := range 5 {
		if _, _, _, err := pw.SignIn(ctx, wrong, sesam.SessionMeta{IP: "192.0.2.1"}); !errors.Is(err, sesam.ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i+1, err)
		}
	}
	// Even the right password is refused now, from any address.
	right := password.Credentials{Identifier: "a@example.com", Password: pass}
	if _, _, _, err := pw.SignIn(ctx, right, sesam.SessionMeta{IP: "198.51.100.1"}); !errors.Is(err, sesam.ErrRateLimited) {
		t.Fatalf("6th attempt: %v", err)
	}
	c.Add(15 * time.Minute)
	if _, _, _, err := pw.SignIn(ctx, right, meta); err != nil {
		t.Fatalf("after the window: %v", err)
	}
	// Success clears the failures for the name.
	for range 4 {
		pw.SignIn(ctx, wrong, meta)
	}
	if _, _, _, err := pw.SignIn(ctx, right, meta); err != nil {
		t.Fatalf("after 4 failures: %v", err)
	}

	// One address trying many names is stopped after 20.
	for i := range 20 {
		id := password.Credentials{Identifier: "user" + string(rune('a'+i)) + "@example.com", Password: pass}
		if _, _, _, err := pw.SignIn(ctx, id, sesam.SessionMeta{IP: "203.0.113.9"}); errors.Is(err, sesam.ErrRateLimited) {
			t.Fatalf("limited after %d names", i)
		}
	}
	if _, _, _, err := pw.SignIn(ctx, right, sesam.SessionMeta{IP: "203.0.113.9"}); !errors.Is(err, sesam.ErrRateLimited) {
		t.Fatalf("21st attempt from one address: %v", err)
	}
}

func TestUsernameMode(t *testing.T) {
	a, pw, _ := setup(t, memadapter.New(), password.Options{Identifier: password.Username, DisableSignUp: true})
	if _, _, _, err := pw.SignUp(ctx, password.SignUpInput{Username: "anna", Password: pass}, meta); !errors.Is(err, sesam.ErrSignUpDisabled) {
		t.Fatalf("sign-up: %v", err)
	}
	u, err := pw.CreateUser(ctx, sesam.NewUser{Username: "Anna"}, pass)
	if err != nil {
		t.Fatal(err)
	}
	if u.Username != "anna" || u.Email != "" {
		t.Fatalf("user %+v", u)
	}
	if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "ANNA", Password: pass}, meta); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Email: "x@example.com"}, pass); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("user without username: %v", err)
	}
	_ = a
}

func TestChangeAndSetPassword(t *testing.T) {
	a, pw, _ := setup(t, memadapter.New(), password.Options{})
	u, err := pw.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}, pass)
	if err != nil {
		t.Fatal(err)
	}
	_, keep, ks, _ := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: pass}, meta)
	_, other, _, _ := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: pass}, meta)

	if err := pw.ChangePassword(ctx, u.ID, "wrong password", "new password 1", ks.ID); !errors.Is(err, sesam.ErrWrongPassword) {
		t.Fatalf("wrong current: %v", err)
	}
	if err := pw.ChangePassword(ctx, u.ID, pass, "short", ks.ID); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("short new: %v", err)
	}
	if err := pw.ChangePassword(ctx, u.ID, pass, "new password 1", ks.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Session(ctx, keep); err != nil {
		t.Fatal("current session ended by password change")
	}
	if _, _, err := a.Session(ctx, other); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatal("other session survived password change")
	}
	if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: pass}, meta); err == nil {
		t.Fatal("old password still works")
	}

	if err := pw.SetPassword(ctx, u.ID, "set by admin 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Session(ctx, keep); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatal("SetPassword kept a session")
	}
	if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: "set by admin 1"}, meta); err != nil {
		t.Fatal(err)
	}

	// A user created without a password gets one.
	nopw, _ := a.CreateUser(ctx, sesam.NewUser{Email: "c@example.com"})
	if ok, _ := pw.HasPassword(ctx, nopw.ID); ok {
		t.Fatal("password without setting one")
	}
	if err := pw.SetPassword(ctx, nopw.ID, pass); err != nil {
		t.Fatal(err)
	}
	if ok, _ := pw.HasPassword(ctx, nopw.ID); !ok {
		t.Fatal("SetPassword did not create the account")
	}
	if err := pw.SetPassword(ctx, "nope", pass); !errors.Is(err, sesam.ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

// Users moved from gpanel keep their passwords.
func TestImportedHash(t *testing.T) {
	a, pw, _ := setup(t, memadapter.New(), password.Options{Identifier: password.Username})
	u, _ := a.CreateUser(ctx, sesam.NewUser{Username: "admin"})
	const gpanelHash = "$argon2id$v=19$m=65536,t=2,p=2$6IM2O4zQ9qunG8BfXBYmCQ$WiHMXjmHv7vHT6wuNZZT67E4xtv7lbfInnWXayAWmHc"
	if err := pw.SetPasswordHash(ctx, u.ID, "plain text"); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("plain text accepted: %v", err)
	}
	if err := pw.SetPasswordHash(ctx, u.ID, gpanelHash); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "admin", Password: "gpanel-password"}, meta); err != nil {
		t.Fatal(err)
	}
}

func TestPasswordReset(t *testing.T) {
	m := &mailer{}
	a, pw, c := setup(t, memadapter.New(), password.Options{
		Mailer:   m,
		ResetURL: func(tok string) string { return "https://app.example.com/reset?token=" + tok },
	})
	u, _ := pw.CreateUser(ctx, sesam.NewUser{Email: "a@example.com"}, pass)
	_, old, _, _ := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: pass}, meta)

	if err := pw.RequestReset(ctx, "nobody@example.com", meta); err != nil {
		t.Fatalf("unknown address must look like success: %v", err)
	}
	if len(m.links) != 0 {
		t.Fatal("mail sent to unknown address")
	}
	if err := pw.RequestReset(ctx, "A@example.com", meta); err != nil {
		t.Fatal(err)
	}
	tok := m.token("a@example.com")
	if err := pw.ResetPassword(ctx, tok, "short"); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("short password: %v", err)
	}
	if err := pw.ResetPassword(ctx, tok, "brand new password"); err != nil {
		t.Fatal(err)
	}
	if err := pw.ResetPassword(ctx, tok, "another password"); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("token used twice: %v", err)
	}
	if _, _, err := a.Session(ctx, old); !errors.Is(err, sesam.ErrUnauthenticated) {
		t.Fatal("session survived reset")
	}
	if got, _ := a.User(ctx, u.ID); !got.EmailVerified {
		t.Fatal("e-mail not marked verified after reset")
	}
	if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: "a@example.com", Password: "brand new password"}, meta); err != nil {
		t.Fatal(err)
	}

	// Links expire.
	pw.RequestReset(ctx, "a@example.com", meta)
	c.Add(time.Hour + time.Second)
	if err := pw.ResetPassword(ctx, m.token("a@example.com"), "brand new password 2"); !errors.Is(err, sesam.ErrInvalidToken) {
		t.Fatalf("expired link: %v", err)
	}
	// At most 3 mails per address and hour.
	c.Add(time.Hour)
	for range 3 {
		if err := pw.RequestReset(ctx, "a@example.com", sesam.SessionMeta{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := pw.RequestReset(ctx, "a@example.com", sesam.SessionMeta{}); !errors.Is(err, sesam.ErrRateLimited) {
		t.Fatalf("4th mail: %v", err)
	}
}

func TestOptionsChecked(t *testing.T) {
	for name, o := range map[string]password.Options{
		"mailer without url": {Mailer: &mailer{}},
		"url without mailer": {ResetURL: func(string) string { return "" }},
		"min above max":      {MinLength: 20, MaxLength: 10},
	} {
		_, err := sesam.New(sesam.Config{Adapter: memadapter.New(), BaseURL: "https://a.example", Plugins: []sesam.Plugin{password.New(o)}})
		if err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, pw, _ := setup(t, memadapter.New(), password.Options{})
	if err := pw.RequestReset(ctx, "a@example.com", meta); err == nil {
		t.Fatal("reset without mailer")
	}
}

func TestEmailOrUsername(t *testing.T) {
	_, pw, _ := setup(t, memadapter.New(), password.Options{Identifier: password.EmailOrUsername})
	// Moved from a system with names only: no e-mail address yet.
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Username: "admin"}, pass); err != nil {
		t.Fatal(err)
	}
	both, err := pw.CreateUser(ctx, sesam.NewUser{Username: "anna", Email: "anna@example.com"}, pass)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Email: "mail-only@example.com"}, pass); err != nil {
		t.Fatal(err)
	}
	if _, err := pw.CreateUser(ctx, sesam.NewUser{Name: "nothing"}, pass); !errors.Is(err, sesam.ErrInvalidInput) {
		t.Fatalf("user without e-mail and username: %v", err)
	}
	for _, id := range []string{"admin", "Anna", "ANNA@example.com", "mail-only@example.com"} {
		if _, _, _, err := pw.SignIn(ctx, password.Credentials{Identifier: id, Password: pass}, meta); err != nil {
			t.Errorf("sign-in as %q: %v", id, err)
		}
	}
	// The username and the e-mail address of one account share one limit.
	for i := range 5 {
		id := "anna"
		if i%2 == 1 {
			id = "anna@example.com"
		}
		pw.SignIn(ctx, password.Credentials{Identifier: id, Password: "wrong password"}, sesam.SessionMeta{IP: "192.0.2.50"})
	}
	_, _, _, err = pw.SignIn(ctx, password.Credentials{Identifier: "anna@example.com", Password: pass}, sesam.SessionMeta{IP: "192.0.2.51"})
	if !errors.Is(err, sesam.ErrRateLimited) {
		t.Fatalf("6th attempt on %s through the other name: %v", both.ID, err)
	}
}
