# Architecture

```
your app ──► sesam (core) ◄── plugins/password, later plugins/…
                │                 (declare tables, add endpoints and hooks)
                ▼
          adapter.Adapter  ◄── adapters/sqladapter (SQLite, Postgres)
                                adapters/memadapter (tests)
```

- **Core** (`package sesam`, the module root): users, sessions, cookies, attempt limits, one-time tokens, hooks, the HTTP handler and middleware. It knows no plugin and no database.
- **Plugins** (`plugins/…`): ways to sign in and features. A plugin declares tables (`Schema`), registers hooks and endpoints (`Init`, `Routes`), and is itself the Go API for the app. Plugins never import each other.
- **Providers** (`providers/…`, planned): definitions of other services (Google, Microsoft, GitHub) for a sign-in-with plugin. They are configuration, not plugins.
- **Adapters** (`adapters/…`): store rows of declared tables. `adaptertest` holds every adapter to the same behaviour.
- **Schema** (`schema`): table declarations. `New` merges the core's and the plugins' declarations, so a plugin can add fields to the users table.

Dependencies only point inward: plugins and adapters import the core packages, never the other way round. An app only downloads what it imports.

## Decisions

- **Embedded, not a service.** sesam runs in the app's process and database. A separate sign-in server can be built later on top of it (a small program plus a plugin that speaks OpenID Connect).
- **Generic adapter instead of typed stores.** With one CRUD interface and declared tables, a new database needs one adapter, not one implementation per plugin. Sign-in needs simple queries only.
- **Authentication only.** Roles and permissions stay in the app (gpanel keeps them in its own service layer).
- **Text ids.** Random, not guessable, and the same in every database.
- **Passwords in `accounts`.** A user's password is one way to sign in among others, like a later Google account.
- **Deleting is done by sesam,** following the declared references, so it does not depend on the database enforcing foreign keys.
