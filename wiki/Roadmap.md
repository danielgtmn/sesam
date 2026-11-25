# Roadmap

## Done

- Core: users, sessions and cookies, attempt limits, one-time tokens, hooks, endpoints, middleware
- Adapters: SQLite and Postgres (`sqladapter`), memory, conformance tests
- Password plugin: e-mail or username, sign-up (can be turned off), change, reset by e-mail link, importing argon2id hashes

## Next

1. **gpanel uses sesam** for its sign-in: users and password hashes move over (passwords and sessions stay valid), roles stay in gpanel.
2. **E-mail confirmation** at sign-up (one-time link).
3. **Two-factor sign-in** (`plugins/twofactor`): authenticator apps and backup codes.
4. **Access tokens** (`plugins/apitoken`) for scripts and CI, so gpanel's `op_…` tokens can move too.

## Later

- Passkeys (`plugins/passkey`)
- Sign-in with other services (`plugins/oauth` with `providers/google`, `providers/microsoft`, `providers/github`, …)
- Organizations with members and roles, also nested (`plugins/organization`)
- Sign-in by e-mail code or link
- A JavaScript client and UI components
- More adapters (MySQL)
- Acting as a sign-in server for other apps (OpenID Connect)
