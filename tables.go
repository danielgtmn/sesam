package sesam

import (
	"time"

	"github.com/danielgtmn/ablage"
)

// Names of the core tables. sesam's tables start with "auth_", so they can
// share a store (and a database) with the app's own tables; plugins name
// theirs the same way. Pass sesam a store without a table prefix to keep
// these names in the database.
const (
	TableUsers         = "auth_users"
	TableSessions      = "auth_sessions"
	TableAccounts      = "auth_accounts"
	TableVerifications = "auth_verifications"
	TableRateLimits    = "auth_rate_limits"
)

// The tables are declared through repositories: the structs below are how
// rows look, and their tags give the indexes. The columns are the ones of
// sesam 1.x, so databases created by it keep working.
//
// References to users are not cascading: tables created by sesam 1.x have
// none, and Migrate never changes a column. DeleteUser deletes sessions,
// accounts and verifications itself; tables of plugins and apps that
// reference users should use ablage.Cascade.

// userRow is a row of auth_users. E-mail address and username are NULL when
// not set, so that several users can be without one.
type userRow struct {
	ID            string
	Email         *string `db:",unique"`
	EmailVerified bool
	Username      *string `db:",unique"`
	Name          string
	CreatedAt     time.Time `db:",index"`
	UpdatedAt     time.Time
}

// sessionRow is a row of auth_sessions. id is the SHA-256 (hex) of the
// token in the cookie; the token itself is never stored.
type sessionRow struct {
	ID         string
	UserID     string `db:",ref=auth_users,index"`
	CreatedAt  time.Time
	ExpiresAt  time.Time `db:",index"`
	LastSeenAt time.Time
	IP         *string
	UserAgent  *string
}

// Account is a way to sign in as a user, a row of auth_accounts. The
// password plugin keeps the password hash in an account with provider
// "credential" (ProviderAccountID is the user id); sign-in with other
// services adds accounts with their provider name.
type Account struct {
	ID                string    `json:"id"`
	UserID            string    `db:",ref=auth_users,index" json:"user_id"`
	Provider          string    `json:"provider"`
	ProviderAccountID string    `json:"provider_account_id"`
	PasswordHash      *string   `json:"-"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// verificationRow is a one-time token (password reset, e-mail
// confirmation), a row of auth_verifications. id is the SHA-256 (hex) of
// the token.
type verificationRow struct {
	ID         string
	Purpose    string
	Identifier string    `db:",index"`
	ExpiresAt  time.Time `db:",index"`
	CreatedAt  time.Time
}

// rateLimitTable holds attempt counters; id is the SHA-256 (hex) of the
// limit's key. It is used through the store directly, because counting
// needs an atomic increment.
var rateLimitTable = ablage.Table{Name: TableRateLimits, Fields: []ablage.Field{
	{Name: "count", Type: ablage.Int},
	{Name: "window_start", Type: ablage.Time, Index: true},
}}

// repos are the repositories of the core tables.
type repos struct {
	users         *ablage.Repository[userRow]
	sessions      *ablage.Repository[sessionRow]
	accounts      *ablage.Repository[Account]
	verifications *ablage.Repository[verificationRow]
}

func newRepos(s ablage.Store) (repos, error) {
	var r repos
	var err error
	if r.users, err = ablage.NewRepository[userRow](s, TableUsers); err != nil {
		return r, err
	}
	if r.sessions, err = ablage.NewRepository[sessionRow](s, TableSessions); err != nil {
		return r, err
	}
	if r.accounts, err = ablage.NewRepository[Account](s, TableAccounts,
		ablage.WithUniqueIndex("provider", "provider_account_id")); err != nil {
		return r, err
	}
	if r.verifications, err = ablage.NewRepository[verificationRow](s, TableVerifications); err != nil {
		return r, err
	}
	return r, s.Declare(rateLimitTable)
}
