package sesam

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"

	"github.com/danielgtmn/sesam/schema"
)

// Plugin adds a way to sign in or other features. Plugins live in
// sesam/plugins; the plugin value itself is the typed Go API, e.g.
// password.New(…).SignIn(…).
type Plugin interface {
	// ID is unique among the plugins of an Auth, e.g. "password".
	ID() string
	// Schema returns the tables the plugin needs, or fields it adds to
	// existing tables. Called once by New.
	Schema() []schema.Table
	// Init is called once by New after all schemas are merged. Plugins keep
	// the Auth to call its methods later, and may register hooks.
	Init(a *Auth) error
}

// RoutesPlugin is a Plugin with HTTP endpoints under Config.BasePath.
type RoutesPlugin interface {
	Plugin
	Routes(r *Router)
}

// Hooks let the app (Config.Hooks) and plugins (Auth.AddHooks) react to
// what happens. Before-hooks can stop the action by returning an error,
// which is passed to the caller as is: return a *Error to show the person a
// message. Unset hooks are skipped.
type Hooks struct {
	// BeforeCreateUser may change the new user or refuse it.
	BeforeCreateUser func(ctx context.Context, u *NewUser) error
	AfterCreateUser  func(ctx context.Context, u User)
	// BeforeSignIn runs after the person proved who they are and before a
	// session is created, e.g. to refuse a locked account.
	BeforeSignIn     func(ctx context.Context, u User) error
	AfterSignIn      func(ctx context.Context, u User, s Session)
	AfterSignOut     func(ctx context.Context, s Session)
	BeforeDeleteUser func(ctx context.Context, u User) error
	AfterDeleteUser  func(ctx context.Context, u User)
}

// AddHooks registers more hooks; they run after the ones already
// registered. Plugins call it in Init.
func (a *Auth) AddHooks(h Hooks) {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	a.hooks = append(a.hooks, h)
}

func (a *Auth) allHooks() []Hooks {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	return append([]Hooks(nil), a.hooks...)
}

// NewID returns a random identifier for a row: 26 characters, 130 bits.
func NewID() string { return rand.Text() }

// NewToken returns a random secret for a cookie or link: 43 characters,
// 256 bits, URL-safe.
func NewToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // never fails, see crypto/rand
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the hex SHA-256 of a token, which is what gets stored.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
