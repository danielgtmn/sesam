# Development

## Prerequisites

- **Go:** the version in `go.mod`.
- **Docker** for the Postgres tests (or any Postgres you point `SESAM_TEST_POSTGRES` at).
- **shellcheck** for `make lint`.

## Make targets

| Target | What it does |
|---|---|
| `make test` | `go vet` and `go test -race` on memory and SQLite; the Postgres tests skip |
| `make postgres` | starts `postgres:18-alpine` in Docker on port 55432 |
| `make test-postgres` | all tests, including Postgres |
| `make postgres-stop` | stops it |
| `make lint` | shellcheck, the commit message check's tests, actionlint |

Postgres tests read `SESAM_TEST_POSTGRES` (a DSN). Each test creates its own schema and drops it afterwards, so they can run against a shared database. CI runs them against a Postgres service and fails if they were skipped.

## Tests

- `adapters/adaptertest`: the conformance suite, run for memory, SQLite (also with one connection) and Postgres.
- The core's tests (`sesam_test.go`) use the memory adapter and a fake clock.
- `plugins/password/http_test.go` drives the endpoints with a real HTTP client and cookie jar on all three databases.
- `examples/basic` is a runnable app for trying changes by hand.

## Conventions

See [AGENTS.md](https://github.com/danielgtmn/sesam/blob/main/AGENTS.md). In short: dependencies point inward, plugins write no SQL, migrations only add, secrets are never stored, errors for people are `*sesam.Error` with a sentence about the effect, new adapter behaviour gets a conformance test.

**Commit messages and pull request titles** follow [Conventional Commits](https://www.conventionalcommits.org/): `<type>(<scope>): <description>`, types `feat`, `fix`, `perf`, `docs`, `chore`, `ci`, `refactor`, `test`, `build`. The required check `commits` checks the title and every commit of a pull request into `main`.

## Releases

A Go module is released by its tag. To release, open a pull request `main → release` and merge it with a merge commit. The `Release` workflow computes the next version from the commits since the last `v*` tag (`fix:`/`perf:` patch, `feat:` minor, `!` or `BREAKING CHANGE:` major), pushes the tag and publishes a GitHub release with notes. Without `feat:`, `fix:` or `perf:` commits nothing is released.

Between releases, apps can use `go get github.com/danielgtmn/sesam@main`.

## The wiki

The `wiki/` folder is copied to the GitHub wiki on every push to `main` that changes it. The wiki must exist once: open the Wiki tab and create any page before the first run.
