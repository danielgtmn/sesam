# Databases

sesam keeps its data with [ablage](https://github.com/danielgtmn/ablage), a small database layer with three stores that behave the same:

| Store | Package | For |
|---|---|---|
| SQL | `github.com/danielgtmn/ablage/sqlstore` | SQLite and Postgres through `database/sql` |
| Memory | `github.com/danielgtmn/ablage/memstore` | Tests |

Plugins never write SQL: they declare their tables (usually as ablage repositories of a struct), and the store creates and queries them. The same store can hold your app's own tables.

## SQLite

```go
db, err := sql.Open("sqlite", "app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
store, err := sqlstore.New(db, sqlstore.SQLite)
```

Tested with `modernc.org/sqlite`; other drivers work too. **Foreign keys must be on** for every connection (`sqlstore.New` checks it). Either start transactions with `BEGIN IMMEDIATE` (`_txlock=immediate` above) or use a single connection (`db.SetMaxOpenConns(1)`), so two transactions never block each other while both wait to write. Times are stored as microseconds since 1970, flags as 0/1.

## Postgres

```go
db, err := sql.Open("pgx", "postgres://…")   // github.com/jackc/pgx/v5/stdlib
store, err := sqlstore.New(db, sqlstore.Postgres)
```

Tables are created in the connection's current schema (`search_path`). When several instances start at once, migrations wait for each other.

## Tables

sesam's tables are named `auth_…`, so they can sit next to your own. Create the store **without** a table prefix (`sqlstore.WithPrefix`) to keep these names; with one, it is put in front (`app_auth_users`).

| Table | Holds |
|---|---|
| `auth_users` | id, email, email_verified, username, name, created_at, updated_at |
| `auth_sessions` | id (SHA-256 of the cookie token), user_id, created_at, expires_at, last_seen_at, ip, user_agent |
| `auth_accounts` | ways to sign in: provider `credential` with password_hash; later sign-in with other services |
| `auth_verifications` | one-time tokens (SHA-256), e.g. password reset |
| `auth_rate_limits` | attempt counters (SHA-256 of the key) |

Plugins add tables of their own, named `auth_<plugin>_…`. IDs are random text (26 characters); store them in your own tables as text, e.g. `owner_id TEXT REFERENCES auth_users(id)`.

## Migrations

`auth.Migrate(ctx)` creates missing tables, fields and indexes and nothing else: no renames, no drops. Run it at every start. It migrates every table declared on the store, so if your app declares its own ablage tables on the same store, one call does both.

If your app manages its migrations itself, print the statements instead:

```go
stmts, err := store.DDL() // after sesam.New
```

Databases created by sesam 1.x keep working as they are; see [[Upgrading to v2|Upgrading-to-v2]].

## Your own data about users

Keep it in your own table keyed by the user's id (e.g. `panel_users(user_id, role)`), and use the hooks to create or remove rows. With ablage, reference the users with cascade (`db:",ref=auth_users,cascade"`) and `DeleteUser` removes your rows too; with your own SQL, use `REFERENCES auth_users(id) ON DELETE CASCADE` or delete them in `BeforeDeleteUser`.
