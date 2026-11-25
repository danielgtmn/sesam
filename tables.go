package sesam

import "github.com/danielgtmn/sesam/schema"

// Names of the core tables, as plugins pass them to the adapter.
const (
	TableUsers         = "users"
	TableSessions      = "sessions"
	TableAccounts      = "accounts"
	TableVerifications = "verifications"
	TableRateLimits    = "rate_limits"
)

var userRef = &schema.Reference{Table: TableUsers}

// coreTables are the tables every sesam instance has. Plugins add their own
// with Plugin.Schema, or fields to these.
var coreTables = []schema.Table{
	{Name: TableUsers, Fields: []schema.Field{
		{Name: "email", Type: schema.String, Unique: true},
		{Name: "email_verified", Type: schema.Bool, Required: true},
		{Name: "username", Type: schema.String, Unique: true},
		{Name: "name", Type: schema.String, Required: true},
		{Name: "created_at", Type: schema.Time, Required: true, Index: true},
		{Name: "updated_at", Type: schema.Time, Required: true},
	}},
	// id is the SHA-256 (hex) of the token in the cookie; the token itself
	// is never stored.
	{Name: TableSessions, Fields: []schema.Field{
		{Name: "user_id", Type: schema.String, Required: true, Ref: userRef},
		{Name: "created_at", Type: schema.Time, Required: true},
		{Name: "expires_at", Type: schema.Time, Required: true, Index: true},
		{Name: "last_seen_at", Type: schema.Time, Required: true},
		{Name: "ip", Type: schema.String},
		{Name: "user_agent", Type: schema.String},
	}},
	// A way to sign in as a user: provider "credential" holds the password
	// hash (provider_account_id is the user id); sign-in with other
	// services adds rows with their provider name.
	{Name: TableAccounts, Fields: []schema.Field{
		{Name: "user_id", Type: schema.String, Required: true, Ref: userRef},
		{Name: "provider", Type: schema.String, Required: true},
		{Name: "provider_account_id", Type: schema.String, Required: true},
		{Name: "password_hash", Type: schema.String},
		{Name: "created_at", Type: schema.Time, Required: true},
		{Name: "updated_at", Type: schema.Time, Required: true},
	}, Indexes: []schema.Index{
		{Fields: []string{"provider", "provider_account_id"}, Unique: true},
	}},
	// One-time tokens (password reset, e-mail confirmation). id is the
	// SHA-256 (hex) of the token.
	{Name: TableVerifications, Fields: []schema.Field{
		{Name: "purpose", Type: schema.String, Required: true},
		{Name: "identifier", Type: schema.String, Required: true, Index: true},
		{Name: "expires_at", Type: schema.Time, Required: true, Index: true},
		{Name: "created_at", Type: schema.Time, Required: true},
	}},
	// Attempt counters; id is the SHA-256 (hex) of the limit's key.
	{Name: TableRateLimits, Fields: []schema.Field{
		{Name: "count", Type: schema.Int, Required: true},
		{Name: "window_start", Type: schema.Time, Required: true, Index: true},
	}},
}
