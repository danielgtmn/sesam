# Databases

sesam stores data through an **adapter**. Plugins never write SQL; they declare tables, and the adapter creates and queries them.

| Adapter | Package | For |
|---|---|---|
| SQL | `adapters/sqladapter` | SQLite and Postgres through `database/sql` |
| Memory | `adapters/memadapter` | Tests |

## SQLite

```go
db, err := sql.Open("sqlite", "app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
store, err := sqladapter.New(db, sqladapter.SQLite)
```

Tested with `modernc.org/sqlite`; other drivers work too. Either start transactions with `BEGIN IMMEDIATE` (`_txlock=immediate` above) or use a single connection (`db.SetMaxOpenConns(1)`), so two transactions never block each other while both wait to write. Times are stored as microseconds since 1970, flags as 0/1.

## Postgres

```go
db, err := sql.Open("pgx", "postgres://…")   // github.com/jackc/pgx/v5/stdlib
store, err := sqladapter.New(db, sqladapter.Postgres)
```

Tables are created in the connection's current schema (`search_path`). When several instances start at once, migrations wait for each other.

## Tables

With the default prefix `auth_` (change with `sqladapter.Options{TablePrefix: "x_"}` or `NoPrefix: true`):

| Table | Holds |
|---|---|
| `auth_users` | id, email, email_verified, username, name, created_at, updated_at |
| `auth_sessions` | id (SHA-256 of the cookie token), user_id, created_at, expires_at, last_seen_at, ip, user_agent |
| `auth_accounts` | ways to sign in: provider `credential` with password_hash; later sign-in with other services |
| `auth_verifications` | one-time tokens (SHA-256), e.g. password reset |
| `auth_rate_limits` | attempt counters (SHA-256 of the key) |

Plugins add tables or fields. IDs are random text (26 characters); store them in your own tables as text, e.g. `owner_id TEXT REFERENCES auth_users(id)`.

## Migrations

`auth.Migrate(ctx)` creates missing tables, fields and indexes and does nothing else: no renames, no drops. Run it at every start. A new required field cannot be added to an existing table, so plugins add fields as optional.

If your app manages its migrations itself, print the statements instead:

```go
stmts, err := store.DDL() // after sesam.New
```

## Your own data about users

Keep it in your own table keyed by the user's id (e.g. `panel_users(user_id, role)`), and use the hooks to create or remove rows. sesam does not know these tables, so delete them in `BeforeDeleteUser`, or reference `auth_users(id)` with `ON DELETE CASCADE`.
