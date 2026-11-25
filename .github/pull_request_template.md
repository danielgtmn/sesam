<!-- Title: Conventional Commits, e.g. "feat(password): add a password strength check". Every commit on main must follow it too. -->

Closes #

## What changes

## How it was tested

- [ ] `go vet ./...` and `go test -race ./...` with `SESAM_TEST_POSTGRES` set (Postgres tests ran, not skipped)
- [ ] New adapter behaviour is covered in `adapters/adaptertest`
- [ ] Tried in an app (e.g. `examples/basic`) for changes to endpoints, cookies or sessions
- [ ] Wiki and README updated where they describe this

## Not tested / open questions
