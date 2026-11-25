// Package sesam is authentication you embed in a Go app: users, sign-in,
// sessions and cookies, with each way of signing in as a plugin (see
// sesam/plugins) and the data in the app's own database (see
// sesam/adapters).
//
// sesam answers "who is this?". What that person may do is up to the app.
//
//	pw := password.New(password.Options{})
//	auth, err := sesam.New(sesam.Config{
//		Adapter: sqlAdapter,
//		BaseURL: "https://app.example.com",
//		Plugins: []sesam.Plugin{pw},
//	})
//	if err := auth.Migrate(ctx); err != nil { … }
//	mux.Handle("/api/auth/", auth.Handler())
//	mux.Handle("/", auth.Middleware(app))
//	// in a handler: s, ok := sesam.FromContext(r.Context())
package sesam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danielgtmn/sesam/adapter"
	"github.com/danielgtmn/sesam/schema"
)

// Config configures an Auth. Adapter and BaseURL are required.
type Config struct {
	// Adapter stores the data, e.g. sqladapter.New(db, sqladapter.SQLite).
	Adapter adapter.Adapter

	// BaseURL is where the app is reached, e.g. "https://app.example.com".
	// Its origin is trusted for changing requests, and with https cookies
	// are marked Secure and get the "__Host-" prefix.
	BaseURL string
	// BasePath is where Handler is mounted. Default "/api/auth".
	BasePath string
	// TrustedOrigins are further origins (scheme://host[:port]) whose pages
	// may send changing requests to Handler.
	TrustedOrigins []string
	// RequireHeader, if set, must be present with a non-empty value on
	// every changing request to Handler and on cookie-authenticated
	// changing requests through Middleware. Browsers do not let other
	// sites set custom headers, so it is a second protection against
	// cross-site requests.
	RequireHeader string

	Session SessionConfig
	Cookie  CookieConfig

	// ClientIP returns the address of the client, used for rate limits
	// and shown with sessions. Default: the host of r.RemoteAddr. Set it
	// when the app runs behind a proxy.
	ClientIP func(r *http.Request) string

	Hooks   Hooks
	Plugins []Plugin

	// SendEmail delivers mail that sesam and plugins write, e.g. password
	// reset links. sesam never sends mail itself: the app sends it with
	// whatever it already uses. Without it, features that need mail are
	// unavailable (see Auth.CanSendEmail).
	SendEmail func(ctx context.Context, e Email) error

	// Logger receives internal errors of Handler. Default slog.Default().
	Logger *slog.Logger
	// Now is the clock. Default time.Now.
	Now func() time.Time
}

// SessionConfig sets how long a sign-in lasts.
type SessionConfig struct {
	// IdleTimeout ends a session that was not used for this long.
	// Default 3 days.
	IdleTimeout time.Duration
	// MaxLifetime ends a session this long after sign-in, used or not.
	// Default 30 days.
	MaxLifetime time.Duration
}

// CookieConfig sets the session cookie.
type CookieConfig struct {
	// Name of the cookie. Default "sesam_session". With an https BaseURL
	// it is sent as "__Host-" + Name.
	Name string
	// SameSite of the cookie. Default http.SameSiteLaxMode.
	SameSite http.SameSite
}

// Auth is a configured sesam instance. Create it with New.
type Auth struct {
	cfg      Config
	db       adapter.Adapter
	tables   []schema.Table
	origins  map[string]bool
	secure   bool
	hooksMu  sync.Mutex
	hooks    []Hooks
	plugins  map[string]Plugin
	router   *Router
	logger   *slog.Logger
	basePath string
}

// New checks the configuration, collects the plugins' tables and
// initializes the plugins. It does not touch the database; call Migrate for
// that.
func New(cfg Config) (*Auth, error) {
	if cfg.Adapter == nil {
		return nil, errors.New("sesam: Config.Adapter is required")
	}
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("sesam: Config.BaseURL must be an http or https URL, got %q", cfg.BaseURL)
	}
	if cfg.BasePath == "" {
		cfg.BasePath = "/api/auth"
	}
	cfg.BasePath = "/" + strings.Trim(cfg.BasePath, "/")
	if cfg.Session.IdleTimeout <= 0 {
		cfg.Session.IdleTimeout = 3 * 24 * time.Hour
	}
	if cfg.Session.MaxLifetime <= 0 {
		cfg.Session.MaxLifetime = 30 * 24 * time.Hour
	}
	if cfg.Session.IdleTimeout > cfg.Session.MaxLifetime {
		cfg.Session.IdleTimeout = cfg.Session.MaxLifetime
	}
	if cfg.Cookie.Name == "" {
		cfg.Cookie.Name = "sesam_session"
	}
	if cfg.Cookie.SameSite == 0 {
		cfg.Cookie.SameSite = http.SameSiteLaxMode
	}
	if cfg.ClientIP == nil {
		cfg.ClientIP = remoteIP
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &Auth{
		cfg:      cfg,
		db:       cfg.Adapter,
		origins:  map[string]bool{origin(base): true},
		secure:   base.Scheme == "https",
		plugins:  map[string]Plugin{},
		logger:   cfg.Logger,
		basePath: cfg.BasePath,
	}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	for _, o := range cfg.TrustedOrigins {
		u, err := url.Parse(o)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return nil, fmt.Errorf("sesam: invalid trusted origin %q", o)
		}
		a.origins[origin(u)] = true
	}
	a.hooks = append(a.hooks, cfg.Hooks)

	decl := append([]schema.Table(nil), coreTables...)
	for _, p := range cfg.Plugins {
		if p == nil {
			return nil, errors.New("sesam: nil plugin")
		}
		if _, dup := a.plugins[p.ID()]; dup {
			return nil, fmt.Errorf("sesam: plugin %q added twice", p.ID())
		}
		a.plugins[p.ID()] = p
		decl = append(decl, p.Schema()...)
	}
	if a.tables, err = schema.Merge(decl...); err != nil {
		return nil, fmt.Errorf("sesam: %w", err)
	}
	if err := a.db.Schema(a.tables); err != nil {
		return nil, fmt.Errorf("sesam: %w", err)
	}
	for _, p := range cfg.Plugins {
		if err := p.Init(a); err != nil {
			return nil, fmt.Errorf("sesam: plugin %q: %w", p.ID(), err)
		}
	}
	a.router = newRouter(a)
	for _, p := range cfg.Plugins {
		if rp, ok := p.(RoutesPlugin); ok {
			rp.Routes(a.router)
		}
	}
	return a, nil
}

// Migrate creates the tables, fields and indexes sesam and the plugins
// need. It only adds and can run at every start.
func (a *Auth) Migrate(ctx context.Context) error { return a.db.Migrate(ctx) }

// Tables returns the complete schema: core tables with the plugins'
// additions.
func (a *Auth) Tables() []schema.Table { return a.tables }

// Adapter returns the adapter, for plugins.
func (a *Auth) Adapter() adapter.Adapter { return a.db }

// Now returns the current time from the configured clock.
func (a *Auth) Now() time.Time { return a.cfg.Now() }

// Config returns the configuration with defaults filled in.
func (a *Auth) Config() Config { return a.cfg }

// Plugin returns the plugin with the given ID.
func (a *Auth) Plugin(id string) (Plugin, bool) {
	p, ok := a.plugins[id]
	return p, ok
}

// DeleteExpired removes expired sessions, one-time tokens and attempt
// counters. Call it now and then, e.g. once an hour.
func (a *Auth) DeleteExpired(ctx context.Context) error {
	now := a.Now()
	if _, err := a.db.Delete(ctx, TableSessions, adapter.Lte("expires_at", now)); err != nil {
		return err
	}
	if _, err := a.db.Delete(ctx, TableSessions, adapter.Lte("created_at", now.Add(-a.cfg.Session.MaxLifetime))); err != nil {
		return err
	}
	if _, err := a.db.Delete(ctx, TableVerifications, adapter.Lte("expires_at", now)); err != nil {
		return err
	}
	// Limits use windows of at most a day in practice; keep a margin.
	_, err := a.db.Delete(ctx, TableRateLimits, adapter.Lte("window_start", now.Add(-7*24*time.Hour)))
	return err
}

func origin(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }
