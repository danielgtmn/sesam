# Architecture

```
your app ──► sesam (core) ◄── plugins/password, later plugins/…
                │                 (declare tables, add endpoints and hooks)
                ▼
          ablage.Store     ◄── ablage/sqlstore (SQLite, Postgres)
      (github.com/danielgtmn/ablage)  ablage/memstore (tests)
```

- **Core** (`package sesam`, the module root): users, sessions, cookies, attempt limits, one-time tokens, hooks, the HTTP handler and middleware. It knows no plugin and no database.
- **Plugins** (`plugins/…`): ways to sign in and features. A plugin declares its tables and registers hooks (`Init`) and endpoints (`Routes`), and is itself the Go API for the app. Plugins never import each other.
- **Providers** (`providers/…`, planned): definitions of other services (Google, Microsoft, GitHub) for a sign-in-with plugin. They are configuration, not plugins.
- **Storage** is [ablage](https://github.com/danielgtmn/ablage), a separate module: typed repositories over a store with the same behaviour on SQLite, Postgres and memory. The core and plugins declare tables on the app's store; the app passes the store in and may keep its own tables there.

Dependencies only point inward: plugins import the core, never the other way round. An app only downloads what it imports.

## Decisions

- **Embedded, not a service.** sesam runs in the app's process and database. A separate sign-in server can be built later on top of it (a small program plus a plugin that speaks OpenID Connect).
- **A shared database layer instead of sesam's own.** Storage started in sesam (v1, `adapter`) and moved into ablage, which other apps use as well. A new database needs one store there, not one implementation per plugin.
- **Authentication only.** Roles and permissions stay in the app (gpanel keeps them in its own service layer).
- **Text ids.** Random, not guessable, and the same in every database.
- **Passwords in `accounts`.** A user's password is one way to sign in among others, like a later Google account.
- **Deleting users:** sesam removes sessions, accounts and one-time tokens itself (the tables of v1 have no cascading foreign keys, and migrations never change a column); tables of plugins reference users with cascade, which every ablage store enforces.
