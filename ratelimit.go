package sesam

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielgtmn/ablage"
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
		now := ablage.NormalizeTime(a.Now())
		start := now.Add(-window)
		// Count in the current window, or start the first one.
		ok, err := a.db.Upsert(ctx, TableRateLimits,
			ablage.Row{"id": id, "count": 1, "window_start": now},
			ablage.Row{"count": ablage.Incr(1)}, ablage.Gt("window_start", start))
		if err != nil {
			return err
		}
		if !ok {
			// The window is over: start a new one. If another request
			// did so in between, count there.
			n, err := a.db.Update(ctx, TableRateLimits, ablage.Row{"count": 1, "window_start": now},
				ablage.And(ablage.Eq("id", id), ablage.Lte("window_start", start)))
			if err != nil {
				return err
			}
			if n == 0 {
				continue
			}
			return nil // 1 attempt, and max is at least 1
		}
		rows, err := a.db.Find(ctx, TableRateLimits, ablage.Query{Where: ablage.Eq("id", id)})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			continue // reset in between
		}
		if count, _ := rows[0]["count"].(int64); count > int64(max) {
			since, _ := rows[0]["window_start"].(time.Time)
			return &RateLimitError{RetryAfter: since.Add(window).Sub(now)}
		}
		return nil
	}
	return errors.New("sesam: rate limit counter is contended")
}

// UndoLimit takes back one attempt counted by Limit under key, e.g. when
// it succeeded but earlier failures under key must keep counting (a limit
// per client address shared by many people). It never goes below zero.
func (a *Auth) UndoLimit(ctx context.Context, key string) error {
	_, err := a.db.Update(ctx, TableRateLimits, ablage.Row{"count": ablage.Incr(-1)},
		ablage.And(ablage.Eq("id", HashToken(key)), ablage.Gt("count", 0)))
	return err
}

// ResetLimit forgets the attempts under key.
func (a *Auth) ResetLimit(ctx context.Context, key string) error {
	_, err := a.db.Delete(ctx, TableRateLimits, ablage.Eq("id", HashToken(key)))
	return err
}

// CreateVerification stores a one-time token for purpose (e.g.
// "password-reset") and identifier (e.g. a user id) and returns it. Earlier
// tokens for the same purpose and identifier stop working.
func (a *Auth) CreateVerification(ctx context.Context, purpose, identifier string, ttl time.Duration) (string, error) {
	token := NewToken()
	now := ablage.NormalizeTime(a.Now())
	err := a.db.Tx(ctx, func(tx ablage.Store) error {
		repo := a.r.verifications.With(tx)
		if _, err := repo.DeleteWhere(ctx, ablage.And(ablage.Eq("purpose", purpose), ablage.Eq("identifier", identifier))); err != nil {
			return err
		}
		return repo.Create(ctx, &verificationRow{
			ID: HashToken(token), Purpose: purpose, Identifier: identifier,
			ExpiresAt: now.Add(ttl), CreatedAt: now,
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
	where := ablage.And(ablage.Eq("id", id), ablage.Eq("purpose", purpose), ablage.Gt("expires_at", a.Now()))
	r, err := a.r.verifications.First(ctx, ablage.Query{Where: where})
	if errors.Is(err, ablage.ErrNotFound) {
		return "", ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	// Only the request that actually deletes the row may use it.
	n, err := a.r.verifications.DeleteWhere(ctx, where)
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrInvalidToken
	}
	return r.Identifier, nil
}
