package sesam

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtmn/sesam/adapter"
)

// Session is a sign-in on one device. ID is not secret: it is the hash of
// the token in the cookie, which cannot be derived from it.
type Session struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	CreatedAt  time.Time `json:"created_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	IP         string    `json:"ip,omitempty"`
	UserAgent  string    `json:"user_agent,omitempty"`
}

// SessionMeta describes the device a session is created from.
type SessionMeta struct {
	IP        string
	UserAgent string
}

// touchEvery limits how often using a session writes to the database.
const touchEvery = time.Minute

func sessionFromRow(r adapter.Row) Session {
	return Session{
		ID:         r.String("id"),
		UserID:     r.String("user_id"),
		CreatedAt:  r.Time("created_at"),
		ExpiresAt:  r.Time("expires_at"),
		LastSeenAt: r.Time("last_seen_at"),
		IP:         r.String("ip"),
		UserAgent:  r.String("user_agent"),
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary.
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// expiry is when a session created at created and used at now ends.
func (a *Auth) expiry(created, now time.Time) time.Time {
	idle := now.Add(a.cfg.Session.IdleTimeout)
	if limit := created.Add(a.cfg.Session.MaxLifetime); limit.Before(idle) {
		return limit
	}
	return idle
}

// SignIn runs the BeforeSignIn hooks, creates a session for u and runs the
// AfterSignIn hooks. Plugins call it once the person proved who they are.
// It returns the token for the cookie.
func (a *Auth) SignIn(ctx context.Context, u User, m SessionMeta) (string, Session, error) {
	for _, h := range a.allHooks() {
		if h.BeforeSignIn != nil {
			if err := h.BeforeSignIn(ctx, u); err != nil {
				return "", Session{}, err
			}
		}
	}
	token, s, err := a.CreateSession(ctx, u.ID, m)
	if err != nil {
		return "", Session{}, err
	}
	for _, h := range a.allHooks() {
		if h.AfterSignIn != nil {
			h.AfterSignIn(ctx, u, s)
		}
	}
	return token, s, nil
}

// CreateSession creates a session without running hooks and returns its
// token. The token is shown only here; only its hash is stored.
func (a *Auth) CreateSession(ctx context.Context, userID string, m SessionMeta) (string, Session, error) {
	now := adapter.NormalizeTime(a.Now())
	token := NewToken()
	s := Session{
		ID: HashToken(token), UserID: userID, CreatedAt: now, ExpiresAt: a.expiry(now, now),
		LastSeenAt: now, IP: truncate(m.IP, 64), UserAgent: truncate(m.UserAgent, 512),
	}
	err := a.db.Create(ctx, TableSessions, adapter.Row{
		"id": s.ID, "user_id": s.UserID, "created_at": s.CreatedAt, "expires_at": s.ExpiresAt,
		"last_seen_at": s.LastSeenAt, "ip": nullable(s.IP), "user_agent": nullable(s.UserAgent),
	})
	if err != nil {
		return "", Session{}, err
	}
	return token, s, nil
}

// Session returns the session for a token and its user, or
// ErrUnauthenticated if there is none or it expired. Using a session
// extends it by the idle timeout, up to the maximum lifetime.
func (a *Auth) Session(ctx context.Context, token string) (Session, User, error) {
	if token == "" || len(token) > 256 {
		return Session{}, User{}, ErrUnauthenticated
	}
	now := a.Now()
	r, err := a.db.FindOne(ctx, TableSessions, adapter.Eq("id", HashToken(token)), adapter.Gt("expires_at", now))
	if errors.Is(err, adapter.ErrNotFound) {
		return Session{}, User{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, User{}, err
	}
	s := sessionFromRow(r)
	if !now.Before(s.CreatedAt.Add(a.cfg.Session.MaxLifetime)) {
		return Session{}, User{}, ErrUnauthenticated
	}
	u, err := a.User(ctx, s.UserID)
	if errors.Is(err, ErrNotFound) {
		return Session{}, User{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, User{}, err
	}
	if now.Sub(s.LastSeenAt) >= touchEvery {
		s.LastSeenAt = adapter.NormalizeTime(now)
		s.ExpiresAt = adapter.NormalizeTime(a.expiry(s.CreatedAt, now))
		if _, err := a.db.Update(ctx, TableSessions, adapter.Row{"last_seen_at": s.LastSeenAt, "expires_at": s.ExpiresAt}, adapter.Eq("id", s.ID)); err != nil {
			return Session{}, User{}, err
		}
	}
	return s, u, nil
}

// Sessions lists the active sessions of a user, newest first.
func (a *Auth) Sessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := a.db.FindMany(ctx, TableSessions, adapter.Query{
		Where:   []adapter.Where{adapter.Eq("user_id", userID), adapter.Gt("expires_at", a.Now())},
		OrderBy: "created_at", Desc: true,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		s := sessionFromRow(r)
		if a.Now().Before(s.CreatedAt.Add(a.cfg.Session.MaxLifetime)) {
			out = append(out, s)
		}
	}
	return out, nil
}

// SignOut ends the session of a token and runs the AfterSignOut hooks. An
// unknown token is not an error.
func (a *Auth) SignOut(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	id := HashToken(token)
	r, err := a.db.FindOne(ctx, TableSessions, adapter.Eq("id", id))
	if errors.Is(err, adapter.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := a.db.Delete(ctx, TableSessions, adapter.Eq("id", id)); err != nil {
		return err
	}
	s := sessionFromRow(r)
	for _, h := range a.allHooks() {
		if h.AfterSignOut != nil {
			h.AfterSignOut(ctx, s)
		}
	}
	return nil
}

// RevokeSession ends one session of a user by its ID. A session of another
// user is ErrNotFound.
func (a *Auth) RevokeSession(ctx context.Context, userID, sessionID string) error {
	n, err := a.db.Delete(ctx, TableSessions, adapter.Eq("id", sessionID), adapter.Eq("user_id", userID))
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeUserSessions ends all sessions of a user except keepID (which may
// be empty) and returns how many ended. Use it after a password change or
// for "sign out everywhere".
func (a *Auth) RevokeUserSessions(ctx context.Context, userID, keepID string) (int64, error) {
	where := []adapter.Where{adapter.Eq("user_id", userID)}
	if keepID != "" {
		where = append(where, adapter.Ne("id", keepID))
	}
	return a.db.Delete(ctx, TableSessions, where...)
}
