package sesam

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtmn/ablage"
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

func (r sessionRow) session() Session {
	return Session{
		ID:         r.ID,
		UserID:     r.UserID,
		CreatedAt:  r.CreatedAt,
		ExpiresAt:  r.ExpiresAt,
		LastSeenAt: r.LastSeenAt,
		IP:         deref(r.IP),
		UserAgent:  deref(r.UserAgent),
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
	now := ablage.NormalizeTime(a.Now())
	token := NewToken()
	s := Session{
		ID: HashToken(token), UserID: userID, CreatedAt: now, ExpiresAt: a.expiry(now, now),
		LastSeenAt: now, IP: truncate(m.IP, 64), UserAgent: truncate(m.UserAgent, 512),
	}
	if err := a.ImportSession(ctx, s); err != nil {
		return "", Session{}, err
	}
	return token, s, nil
}

// ImportSession stores a session as it is, without running hooks, e.g. to
// take over sessions of a sign-in an app had before sesam. s.ID must be
// HashToken of the token in the person's cookie, or Session finds it
// never; the user must exist (else an error matching ablage.ErrReference).
// A session with this ID already stored gives an error matching
// ablage.ErrDuplicate.
func (a *Auth) ImportSession(ctx context.Context, s Session) error {
	if s.ID == "" || s.UserID == "" {
		return InvalidInput("A session needs the hash of its token and a user.")
	}
	return a.r.sessions.Create(ctx, &sessionRow{
		ID: s.ID, UserID: s.UserID, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt,
		LastSeenAt: s.LastSeenAt, IP: nullable(s.IP), UserAgent: nullable(s.UserAgent),
	})
}

// Session returns the session for a token and its user, or
// ErrUnauthenticated if there is none or it expired. Using a session
// extends it by the idle timeout, up to the maximum lifetime.
func (a *Auth) Session(ctx context.Context, token string) (Session, User, error) {
	if token == "" || len(token) > 256 {
		return Session{}, User{}, ErrUnauthenticated
	}
	now := a.Now()
	r, err := a.r.sessions.First(ctx, ablage.Query{Where: ablage.And(ablage.Eq("id", HashToken(token)), ablage.Gt("expires_at", now))})
	if errors.Is(err, ablage.ErrNotFound) {
		return Session{}, User{}, ErrUnauthenticated
	}
	if err != nil {
		return Session{}, User{}, err
	}
	s := r.session()
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
		s.LastSeenAt = ablage.NormalizeTime(now)
		s.ExpiresAt = ablage.NormalizeTime(a.expiry(s.CreatedAt, now))
		err := a.r.sessions.Patch(ctx, s.ID, ablage.Row{"last_seen_at": s.LastSeenAt, "expires_at": s.ExpiresAt})
		if errors.Is(err, ablage.ErrNotFound) { // signed out in between
			return Session{}, User{}, ErrUnauthenticated
		}
		if err != nil {
			return Session{}, User{}, err
		}
	}
	return s, u, nil
}

// Sessions lists the active sessions of a user, newest first.
func (a *Auth) Sessions(ctx context.Context, userID string) ([]Session, error) {
	rows, err := a.r.sessions.Find(ctx, ablage.Query{
		Where:   ablage.And(ablage.Eq("user_id", userID), ablage.Gt("expires_at", a.Now())),
		OrderBy: []ablage.Order{ablage.Desc("created_at")},
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		s := r.session()
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
	r, err := a.r.sessions.Get(ctx, id)
	if errors.Is(err, ablage.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	n, err := a.r.sessions.DeleteWhere(ctx, ablage.Eq("id", id))
	if err != nil || n == 0 { // n == 0: signed out in between
		return err
	}
	s := r.session()
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
	n, err := a.r.sessions.DeleteWhere(ctx, ablage.And(ablage.Eq("id", sessionID), ablage.Eq("user_id", userID)))
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
	where := ablage.Eq("user_id", userID)
	if keepID != "" {
		where = ablage.And(where, ablage.Ne("id", keepID))
	}
	return a.r.sessions.DeleteWhere(ctx, where)
}
