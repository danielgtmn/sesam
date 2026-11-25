# Contributing to sesam

Thanks for helping. sesam is a small project, so a short heads-up before larger work saves everyone time.

## Before you start

- **Questions and ideas:** [Discussions](https://github.com/danielgtmn/sesam/discussions).
- **Bugs and concrete features:** [issues](https://github.com/danielgtmn/sesam/issues/new/choose). For anything bigger than a small fix, and for every new plugin or adapter, comment on the issue (or open one) first, so we can agree on the approach.
- **Security problems:** never in public, see [SECURITY.md](SECURITY.md).

## Setting up

You need Go (the version in `go.mod`) and, for the Postgres tests, Docker.

```sh
make test            # memory and SQLite
make postgres        # Postgres in Docker on port 55432
make test-postgres   # all databases
```

## Making a change

1. Fork, then branch from `main`.
2. Read [AGENTS.md](AGENTS.md): layout, the rules that are easy to break (dependencies point inward, plugins write no SQL, migrations only add, secrets never stored or logged), and the commands.
3. Add tests with the change. Adapter behaviour goes into `adapters/adaptertest`, so every adapter is held to it. Update the documentation in `wiki/` (and the README, where it covers the same thing) in the same pull request.
4. Run `make test-postgres` and `make lint`.
5. **Try it for real.** For changes to endpoints, cookies or sessions, run `examples/basic` and go through the flow. If something could not be tested end to end, say so in the pull request.

## Pull requests

- The title follows [Conventional Commits](https://www.conventionalcommits.org): `feat: …`, `fix: …`, `docs: …`, `chore: …`, with an optional scope like `feat(password): …`. The same applies to every commit. The `commits` check enforces it, and release versions are computed from it.
- Reference the issue (`Closes #12`) and fill in the template.
- Pull requests are merged with a merge commit, so keep the commits readable.

## Messages for people

Errors shown to people (`*sesam.Error`) describe what happened for them, never the technology behind it: "This link is invalid or has expired", not "verification not found". No library, protocol or internal names.

## License

sesam is licensed under the [Apache License 2.0](LICENSE). By contributing, you agree that your contribution is licensed under the same terms, and that you have the right to contribute it.

Everyone taking part follows the [Code of Conduct](CODE_OF_CONDUCT.md).
