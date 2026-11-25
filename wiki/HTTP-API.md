# HTTP API

`auth.Handler()` serves these endpoints under `Config.BasePath` (default `/api/auth`). Bodies are JSON (at most 64 KiB, unknown fields are refused). Errors are

```json
{"error": "The name or password is wrong.", "code": "invalid_credentials"}
```

with the matching status; `error` is a sentence to show, `code` is stable. Too many attempts answer `429` with `Retry-After`. Every answer has `Cache-Control: no-store`.

**Changing requests** (`POST`, …) must come from `BaseURL`'s origin or one in `TrustedOrigins` when the browser sends `Origin`, and carry `Config.RequireHeader` if that is set. Otherwise: `403 forbidden_origin`.

## Core

| Method | Path | Body | Answer |
|---|---|---|---|
| `GET` | `/session` | | `200 {"user", "session"}` or `401` |
| `POST` | `/sign-out` | | `204`, removes the cookie |
| `GET` | `/sessions` | | `200 {"sessions": [{…, "current": true}]}` |
| `POST` | `/sessions/revoke` | `{"id"}` | `204`; `404` for a session that is not yours |
| `POST` | `/sessions/revoke-others` | | `204` |

## Password plugin

| Method | Path | Body | Answer |
|---|---|---|---|
| `POST` | `/sign-up` | `{"email", "username", "name", "password"}` | `201 {"user", "session"}`, sets the cookie; `403 sign_up_disabled`, `409 email_taken` / `username_taken`, `400 invalid_input` |
| `POST` | `/sign-in` | `{"email", "password"}` or `{"username", "password"}` (with `EmailOrUsername`, either field may hold either) | `200 {"user", "session"}`, sets the cookie; `401 invalid_credentials`, `429` |
| `POST` | `/change-password` | `{"current", "new"}` | `204`, signed in; `403 wrong_password` |
| `POST` | `/request-password-reset` | `{"email"}` | `204`, also for unknown addresses; `404 reset_disabled` without a mailer |
| `POST` | `/reset-password` | `{"token", "password"}` | `204`; `400 invalid_token` |

## Objects

```json
{"user": {"id": "3N3SQEKV4LWX3S3IUQIRBQLVVC", "email": "anna@example.com", "email_verified": false,
          "username": "anna", "name": "Anna", "created_at": "…", "updated_at": "…"},
 "session": {"id": "465061fb…", "user_id": "3N3S…", "created_at": "…", "expires_at": "…",
             "last_seen_at": "…", "ip": "192.0.2.7", "user_agent": "…"}}
```

The session `id` is not the cookie's secret (it is its SHA-256) and can be shown safely.

## From a browser

```js
await fetch("/api/auth/sign-in", {
  method: "POST",
  headers: {"Content-Type": "application/json"},
  body: JSON.stringify({email, password}),
}); // same origin: the cookie is set and sent automatically
```
