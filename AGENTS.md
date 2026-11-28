# AGENTS.md

Guidance for AI coding agents (and people) working on sesam. User
documentation lives in [`wiki/`](wiki/) and is copied to the GitHub wiki by
a workflow: edit it there.

## What this is

sesam is authentication you embed in a Go app, in the spirit of Better
Auth: a small core (users, sessions, cookies, attempt limits, one-time
tokens, hooks, HTTP endpoints), each way of signing in as a plugin, and the
data in the app's own database through
[ablage](https://github.com/danielgtmn/ablage) (`~/Development/ablage`
locally), a separate module of the same maintainer. It answers "who is
this?"; what a person may do is decided by the app. The first app using it
is gpanel ([ownpanel](https://github.com/danielgtmn/ownpanel)).

## Layout

```
*.go (root)            package sesam: the core. New, Config, users, sessions, cookies,
                       Limit, verifications, hooks, Handler, Middleware; tables.go
                       declares the auth_* tables (ablage repositories)
plugins/password/      sign-in with e-mail or username and password
internal/v1compat/     own module: databases made by sesam v1.0.0 keep working
internal/argon/        argon2id hashing (same format as gpanel)
examples/basic/        a minimal app with SQLite
wiki/                  documentation (synced to the GitHub wiki)
scripts/               commit-type.sh (Conventional Commits check) and its test
```

Later: more plugins under `plugins/` (two-factor, passkeys, organizations,
sign-in with other services) and their service definitions under
`providers/` (Google, Microsoft, …). See wiki/Roadmap.md.

## Rules that are easy to break

- **Module path is `github.com/danielgtmn/sesam/v2`.** A breaking release
  (`!`) that would tag v3 must change the path to `/v3` in the same PR,
  else Go cannot import it.
- **Dependencies point inward.** Plugins import the core; the core never
  imports a plugin. Plugins never import each other; they cooperate
  through hooks.
- **Plugins write no SQL.** They declare their tables on `Auth.Store()` in
  `Init` (ablage repositories, tables named `auth_<plugin>_…`, references to
  users with `cascade`). What ablage lacks is added to ablage (its own PR,
  release, then bump here), never worked around in sesam.
- **Databases of earlier versions keep working.** Table and column names,
  types and stored values of sesam 1.x stay readable; `internal/v1compat`
  proves it on SQLite and Postgres. sesam's core tables keep references to
  users without cascade (v1 tables have none), so `DeleteUser` removes
  sessions, accounts and verifications itself.
- **No database drivers in non-test code** of the module (examples aside).
  Apps bring their own driver and pass a `*sql.DB`.
- **Migrations only add.** `Migrate` creates tables, fields and indexes.
  Never rename or drop a released field.
- **Inside `Store.Tx`, use the store passed to the function** (and
  `repo.With(tx)`). With SQLite and one connection, or memory, using the
  outer store there waits forever.
- **Secrets** (passwords, session tokens, reset tokens) are never stored,
  logged or returned after creation; only hashes are stored (argon2id for
  passwords, SHA-256 for tokens). Error messages never reveal whether an
  account exists.
- **Errors for people are `*sesam.Error`** with a readable sentence that
  describes the effect, never the technology ("This link is invalid or has
  expired", not "verification row not found"). Everything else is an
  internal error and is logged, not shown.
- **New dependencies** need a good reason; prefer the standard library. Pin
  GitHub Actions to commit SHAs with a version comment. No GPL/AGPL
  dependencies: sesam is Apache-2.0.

## Commands

```sh
make test            # go vet + go test -race (memory and SQLite; Postgres tests skip), also internal/v1compat
make postgres        # throwaway Postgres 18 in Docker on port 55432
make test-postgres   # everything, including Postgres
make postgres-stop
make lint            # shellcheck, script tests, actionlint
go run ./examples/basic   # try it: see the comment at the top of the file
```

Postgres tests need `SESAM_TEST_POSTGRES` (a DSN); each test runs in its own
schema, which is dropped afterwards. CI runs them and fails if they skip.

## Done means tested

Run `make test-postgres` for what you touched; new storage behaviour gets a
test in ablage's `storetest`, new endpoints a test with a real HTTP client (see
`plugins/password/http_test.go`). For changes that affect how an app uses
sesam, run `examples/basic` and go through the flow with curl or a browser.
If something could not be tested end to end, say so explicitly.

## Git, pull requests, releases

- `main` is protected: pull requests only, merged with a **merge commit**.
  Required checks: `go`, `lint`, `commits`. CI skips jobs whose files did
  not change.
- **Conventional Commits** for every commit on `main` and every PR title
  (`feat:`, `fix:`, `perf:`, `docs:`, `chore:`, `ci:`, `refactor:`,
  `test:`, `build:`, optional scope and `!`). The `commits` check enforces it.
- **Releases are made by hand from `main`:** run the Release workflow
  (`gh workflow run release.yml --ref main`). It tags the next version
  computed from the commits (`fix:` patch, `feat:` minor, `!` major) and
  publishes a GitHub release. Apps then `go get github.com/danielgtmn/sesam/v2@vX.Y.Z`.
  There is no release branch. A pushed version is permanent (the Go module
  proxy keeps it), so release on purpose, not on every merge.
- Update the wiki (and README where it covers the same) in the same PR as
  the behaviour it describes.
