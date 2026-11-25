# Roadmap

## Done

- Core: users, sessions and cookies, attempt limits, one-time tokens, hooks, endpoints, middleware
- Adapters: SQLite and Postgres (`sqladapter`), memory, conformance tests
- Password plugin: e-mail or username, sign-up (can be turned off), change, reset by e-mail link, importing argon2id hashes
- gpanel signs in through sesam ([ownpanel#81](https://github.com/danielgtmn/ownpanel/pull/81))

Every item below has an [issue](https://github.com/danielgtmn/sesam/issues).

## Next

1. **Attempt limit per account** that strangers cannot use to lock someone out ([#9](https://github.com/danielgtmn/sesam/issues/9)).
2. **E-mail confirmation** at sign-up and on change (one-time link, [#12](https://github.com/danielgtmn/sesam/issues/12)).
3. **Two-factor sign-in** (`plugins/twofactor`, [#13](https://github.com/danielgtmn/sesam/issues/13)): authenticator apps and backup codes.
4. **Access tokens** (`plugins/apitoken`, [#14](https://github.com/danielgtmn/sesam/issues/14)) for scripts and CI, so gpanel's `op_…` tokens can move too.

## Later

- Passkeys (`plugins/passkey`)
- Sign-in with other services (`plugins/oauth` with `providers/google`, `providers/microsoft`, `providers/github`, …)
- Organizations with members and roles, also nested (`plugins/organization`)
- Sign-in by e-mail code or link
- A JavaScript client and UI components
- More adapters (MySQL)
- Acting as a sign-in server for other apps (OpenID Connect)
