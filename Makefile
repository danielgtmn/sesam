.PHONY: test test-postgres postgres postgres-stop lint

# Unit tests on memory and SQLite; the Postgres tests skip.
# internal/v1compat is a module of its own (it imports sesam v1).
test:
	go vet ./... && go test -race ./...
	cd internal/v1compat && go vet ./... && go test -race ./...

# Everything, against a throwaway Postgres in Docker (make postgres). It takes
# connections without a password and listens only on this machine.
PG_DSN := postgres://postgres@localhost:55432/postgres?sslmode=disable
test-postgres:
	go vet ./... && SESAM_TEST_POSTGRES='$(PG_DSN)' go test -race -count=1 ./...
	cd internal/v1compat && go vet ./... && SESAM_TEST_POSTGRES='$(PG_DSN)' go test -race -count=1 ./...

postgres:
	docker run -d --rm --name sesam-pg -e POSTGRES_HOST_AUTH_METHOD=trust -p 127.0.0.1:55432:5432 postgres:18-alpine
	@until docker exec sesam-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done

postgres-stop:
	docker stop sesam-pg

lint:
	shellcheck scripts/*.sh && scripts/commit-type_test.sh && go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
