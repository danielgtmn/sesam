package sesam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtmn/sesam/adapter"
)

// Limit counts an attempt under key and returns a *RateLimitError once more
// than max attempts were made within window. Counters are stored, so they
// survive restarts and are shared by all instances of the app. The window
// starts with the first attempt and must be at most 7 days.
//
// Count before doing the work, and call ResetLimit when an attempt
// succeeded, so that only failures add up.
func (a *Auth) Limit(ctx context.Context, key string, max int, window time.Duration) error {
	if max < 1 || window <= 0 || window > 7*24*time.Hour {
		return fmt.Errorf("sesam: invalid limit %d per %v", max, window)
	}
	id := HashToken(key)
	for range 3 {
		now := a.Now()
		n, err := a.db.Update(ctx, TableRateLimits, adapter.Row{"count": adapter.Incr(1)},
			adapter.Eq("id", id), adapter.Gt("window_start", now.Add(-window)))
		if err != nil {
			return err
		}
		if n == 1 {
			r, err := a.db.FindOne(ctx, TableRateLimits, adapter.Eq("id", id))
			if err != nil {
				return err
			}
			if r.Int("count") > int64(max) {
				return &RateLimitError{RetryAfter: r.Time("window_start").Add(window).Sub(now)}
			}
			return nil
		}
		// No current window: drop an old one and start a new one. If
		// another request started it in between, count there.
		if _, err := a.db.Delete(ctx, TableRateLimits, adapter.Eq("id", id), adapter.Lte("window_start", now.Add(-window))); err != nil {
			return err
		}
		err = a.db.Create(ctx, TableRateLimits, adapter.Row{"id": id, "count": 1, "window_start": now})
		if errors.Is(err, adapter.ErrConflict) {
			continue
		}
		return err
	}
	return errors.New("sesam: rate limit counter is contended")
}

// UndoLimit takes back one attempt counted by Limit under key, e.g. when
// it succeeded but earlier failures under key must keep counting (a limit
// per client address shared by many people). It never goes below zero.
func (a *Auth) UndoLimit(ctx context.Context, key string) error {
	_, err := a.db.Update(ctx, TableRateLimits, adapter.Row{"count": adapter.Incr(-1)},
		adapter.Eq("id", HashToken(key)), adapter.Gt("count", 0))
	return err
}

// ResetLimit forgets the attempts under key.
func (a *Auth) ResetLimit(ctx context.Context, key string) error {
	_, err := a.db.Delete(ctx, TableRateLimits, adapter.Eq("id", HashToken(key)))
	return err
}

// CreateVerification stores a one-time token for purpose (e.g.
// "password-reset") and identifier (e.g. a user id) and returns it. Earlier
// tokens for the same purpose and identifier stop working.
func (a *Auth) CreateVerification(ctx context.Context, purpose, identifier string, ttl time.Duration) (string, error) {
	token := NewToken()
	now := adapter.NormalizeTime(a.Now())
	err := a.db.Tx(ctx, func(tx adapter.Adapter) error {
		if _, err := tx.Delete(ctx, TableVerifications, adapter.Eq("purpose", purpose), adapter.Eq("identifier", identifier)); err != nil {
			return err
		}
		return tx.Create(ctx, TableVerifications, adapter.Row{
			"id": HashToken(token), "purpose": purpose, "identifier": identifier,
			"expires_at": now.Add(ttl), "created_at": now,
		})
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// ConsumeVerification checks a one-time token for purpose, deletes it and
// returns its identifier. A token works only once, even when two requests
// use it at the same moment. Unknown or expired tokens give ErrInvalidToken.
func (a *Auth) ConsumeVerification(ctx context.Context, purpose, token string) (string, error) {
	if token == "" || len(token) > 256 {
		return "", ErrInvalidToken
	}
	id := HashToken(token)
	where := []adapter.Where{adapter.Eq("id", id), adapter.Eq("purpose", purpose), adapter.Gt("expires_at", a.Now())}
	r, err := a.db.FindOne(ctx, TableVerifications, where...)
	if errors.Is(err, adapter.ErrNotFound) {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	// Only the request that actually deletes the row may use it.
	n, err := a.db.Delete(ctx, TableVerifications, where...)
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrInvalidToken
	}
	return r.String("identifier"), nil
}
