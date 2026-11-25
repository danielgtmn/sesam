# AGENTS.md

Guidance for AI coding agents (and people) working on sesam. User
documentation lives in [`wiki/`](wiki/) and is copied to the GitHub wiki by
a workflow: edit it there.

## What this is

sesam is authentication you embed in a Go app, in the spirit of Better
Auth: a small core (users, sessions, cookies, attempt limits, one-time
tokens, hooks, HTTP endpoints), each way of signing in as a plugin, and the
data in the app's own database through an adapter. It answers "who is
this?"; what a person may do is decided by the app. The first app using it
is gpanel ([ownpanel](https://github.com/danielgtmn/ownpanel)).

## Layout

```
*.go (root)            package sesam: the core. New, Config, users, sessions, cookies,
                       Limit, verifications, hooks, Handler, Middleware
schema/                declaring tables and fields; Merge, Dependents
adapter/               the Adapter interface, Row, Where, Incr, shared value checks
adapters/sqladapter/   database/sql with the SQLite and Postgres dialects, Migrate, DDL
adapters/memadapter/   in memory, for tests
adapters/adaptertest/  conformance tests every adapter must pass
plugins/password/      sign-in with e-mail or username and password
internal/argon/        argon2id hashing (same format as gpanel)
examples/basic/        a minimal app with SQLite
wiki/                  documentation (synced to the GitHub wiki)
scripts/               commit-type.sh (Conventional Commits check) and its test
```

Later: more plugins under `plugins/` (two-factor, passkeys, organizations,
sign-in with other services) and their service definitions under
`providers/` (Google, Microsoft, …). See wiki/Roadmap.md.

## Rules that are easy to break

- **Dependencies point inward.** Plugins and adapters import the core; the
  core never imports a plugin or adapter. Plugins never import each other;
  they cooperate through hooks.
- **Plugins write no SQL.** They declare tables in `Schema()` and use the
  adapter. Anything an adapter must support goes into `adapter`, into every
  adapter, and into `adaptertest` in the same change.
- **No database drivers in non-test code** of the module (examples aside).
  Apps bring their own driver and pass a `*sql.DB`.
- **Migrations only add.** `Migrate` creates tables, nullable fields and
  indexes. Never rename or drop a released field; a required field cannot be
  added to an existing table.
- **Inside `Adapter.Tx`, use the adapter passed to the function** and return
  on the first error (Postgres aborts the transaction). With SQLite and one
  connection, using the outer adapter there deadlocks.
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
make test            # go vet + go test -race (memory and SQLite; Postgres tests skip)
make postgres        # throwaway Postgres 18 in Docker on port 55432
make test-postgres   # everything, including Postgres
make postgres-stop
make lint            # shellcheck, script tests, actionlint
go run ./examples/basic   # try it: see the comment at the top of the file
```

Postgres tests need `SESAM_TEST_POSTGRES` (a DSN); each test runs in its own
schema, which is dropped afterwards. CI runs them and fails if they skip.

## Done means tested

Run `make test-postgres` for what you touched; new adapter behaviour gets a
test in `adaptertest`, new endpoints a test with a real HTTP client (see
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
- Merging `main` into `release` tags a new version computed from the
  commits (`fix:` patch, `feat:` minor, `!` major) and publishes a GitHub
  release. Before 1.0, breaking changes are expected; mark them with `!`.
- Update the wiki (and README where it covers the same) in the same PR as
  the behaviour it describes.
