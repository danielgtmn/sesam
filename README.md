# sesam

Authentication you embed in your Go app. Users, sign-in, sessions and
cookies, with each way of signing in as a plugin and the data in your own
database. Inspired by [Better Auth](https://www.better-auth.com), written
for Go.

```go
db, _ := sql.Open("sqlite", "app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_txlock=immediate")
store, _ := sqlstore.New(db, sqlstore.SQLite) // github.com/danielgtmn/ablage

pw := password.New(password.Options{})
auth, err := sesam.New(sesam.Config{
    Store:   store,
    BaseURL: "https://app.example.com",
    Plugins: []sesam.Plugin{pw},
})
if err := auth.Migrate(ctx); err != nil { … }

mux.Handle("/api/auth/", auth.Handler())          // sign-up, sign-in, sign-out, sessions, reset
mux.Handle("/", auth.Middleware(app))             // finds the signed-in user

// in a handler
if cur, ok := sesam.FromContext(r.Context()); ok {
    fmt.Fprintf(w, "Hello %s", cur.User.Email)
}
```

## Status

**Early.** The core and the password plugin work and are tested on SQLite,
Postgres and memory. The API may still
change before 1.0. Two-factor sign-in, passkeys, organizations and sign-in
with other services are planned (see the [roadmap](https://github.com/danielgtmn/sesam/wiki/Roadmap)).

## What you get

- **Users** with an e-mail address, a username, or both.
- **Sessions** in an HttpOnly cookie (`__Host-` on https). Only a hash of
  the token is stored. Sessions end after a time without use and after a
  maximum lifetime; people can list and end their sessions.
- **Password sign-in** with argon2id, the same error and the same time for
  unknown names and wrong passwords, attempt limits per name and per
  client that survive restarts, password change (ends other sessions) and
  reset by e-mail link (you send the mail).
- **Protection against other sites:** changing requests must come from your
  origin or ones you trust, optionally with a header of your choice.
- **Your database:** SQLite or Postgres through `database/sql`, with the
  driver you already use, via [ablage](https://github.com/danielgtmn/ablage).
  Tables are named `auth_users`, … and created by `Migrate`, which only
  ever adds. Or print the SQL with `DDL()` and run it with your own
  migrations.
- **Plugins and hooks:** plugins declare their tables and add endpoints;
  hooks let the app react to or refuse sign-ups, sign-ins and deletions.

## Packages

| Package | What it is |
|---|---|
| `github.com/danielgtmn/sesam/v2` | the core |
| `…/v2/plugins/password` | e-mail or username and password |
| `github.com/danielgtmn/ablage/sqlstore` | SQLite and Postgres |
| `github.com/danielgtmn/ablage/memstore` | in memory, for tests |

Coming from v1: see [Upgrading to v2](https://github.com/danielgtmn/sesam/wiki/Upgrading-to-v2);
databases created by v1 keep working.

## Documentation

The [wiki](https://github.com/danielgtmn/sesam/wiki): getting started,
configuration, the endpoints, databases, writing plugins, security.

## Development

```sh
make test            # memory and SQLite
make postgres        # Postgres in Docker
make test-postgres   # all databases
```

See [AGENTS.md](AGENTS.md) and [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[Apache-2.0](LICENSE).
