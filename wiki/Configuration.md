# Configuration

`sesam.New(sesam.Config{…})` checks the configuration and fills in defaults. It does not touch the database; `Migrate` does.

| Field | Default | Meaning |
|---|---|---|
| `Store` | required | Where data is stored, an `ablage.Store` (SQLite, Postgres or memory), see [[Databases|Databases]]. Without a table prefix, so sesam's tables are named `auth_…`. |
| `BaseURL` | required | Where the app is reached, e.g. `https://app.example.com`. Its origin may send changing requests. With `https`, cookies are `Secure` and named `__Host-…`. |
| `BasePath` | `/api/auth` | Where `Handler` is mounted. |
| `TrustedOrigins` | none | More origins (`scheme://host[:port]`) whose pages may send changing requests, e.g. a dev server `http://localhost:5173`. |
| `RequireHeader` | none | A header (e.g. `X-App-Request`) that every changing request to the endpoints, and every changing request with a session cookie through `Middleware`, must carry with any non-empty value. A second protection against requests from other sites. |
| `Session.IdleTimeout` | 3 days | A session not used for this long ends. Using it extends it (at most once a minute is written). |
| `Session.MaxLifetime` | 30 days | A session ends this long after sign-in, used or not. The cookie lasts as long. |
| `Cookie.Name` | `sesam_session` | Name of the session cookie (`__Host-` is added with https). |
| `Cookie.SameSite` | `Lax` | `SameSite` of the cookie. `Strict` is stricter, but a link from another site then arrives signed out. |
| `ClientIP` | host of `RemoteAddr` | Returns the client's address, for attempt limits and the session list. **Set it when the app runs behind a proxy**, reading the header your proxy sets, or all clients share one limit. |
| `SendEmail` | none | `func(ctx, sesam.Email) error` that delivers mail sesam and plugins write (password reset links, later e-mail confirmation). **sesam never sends mail itself**: your app delivers it with whatever it already uses (its mail server, an API, a queue). `Email` has `To`, `Subject`, `Text` and `HTML` (may be empty). Without it, features that need mail are not offered; `auth.CanSendEmail()` tells. An error you return is logged, never shown to the person. |
| `Hooks` | none | See below. |
| `Plugins` | none | Each plugin once. |
| `Logger` | `slog.Default()` | Receives internal errors of the endpoints. |
| `Now` | `time.Now` | The clock, for tests. |

## Hooks

Before-hooks can refuse the action by returning an error, which reaches the caller unchanged. Return a `*sesam.Error` (or `sesam.InvalidInput("…")`) to show the person a message.

| Hook | When |
|---|---|
| `BeforeCreateUser(ctx, *NewUser) error` | Before a user is created; may change it. |
| `AfterCreateUser(ctx, User)` | After the user is stored. |
| `BeforeSignIn(ctx, User) error` | After the person proved who they are, before the session is created, e.g. to refuse a locked account. |
| `AfterSignIn(ctx, User, Session)` | After sign-in, e.g. for an activity log. |
| `AfterSignOut(ctx, Session)` | After a sign-out through `SignOut`. |
| `BeforeDeleteUser(ctx, User) error` / `AfterDeleteUser(ctx, User)` | Around `DeleteUser`. |

Plugins register their hooks with `auth.AddHooks`.

## Users

| Method | |
|---|---|
| `CreateUser(ctx, NewUser)` | A user without a way to sign in. Plugins offer variants that add one, e.g. `password.CreateUser`. |
| `User`, `UserByEmail`, `UserByUsername` | Look up; `sesam.ErrNotFound` if there is none. |
| `Users(ctx, ListOptions)`, `CountUsers` | List oldest first; count, e.g. to see whether the first account still has to be created. |
| `UpdateUser(ctx, id, UserUpdate)` | Change fields; a new e-mail address is not verified. |
| `DeleteUser(ctx, id)` | Deletes the user with sessions, accounts and plugin rows that reference it. |

E-mail addresses and usernames are stored in lower case. Usernames have 3 to 32 characters: `a-z`, digits, `.`, `_`, `-`. A user needs at least one of the two.

## Sessions

| Method | |
|---|---|
| `Session(ctx, token)` | The session and user for a cookie token, or `ErrUnauthenticated`. |
| `Sessions(ctx, userID)` | Active sessions, newest first. |
| `SignOut(ctx, token)` | Ends one session (runs `AfterSignOut`). |
| `RevokeSession(ctx, userID, sessionID)` | Ends a session of that user. |
| `RevokeUserSessions(ctx, userID, keepID)` | "Sign out everywhere" (except `keepID`). |
| `SetSessionCookie`, `ClearSessionCookie`, `SessionToken`, `SessionFromRequest` | Cookie helpers. |
| `Middleware`, `RequireSession`, `FromContext` | For your handlers. |

## Errors

Errors meant for people are `*sesam.Error` with `Status`, a stable `Code` and a readable `Message`. Compare with `errors.Is(err, sesam.ErrInvalidCredentials)` and so on. Too many attempts give a `*sesam.RateLimitError` with `RetryAfter`, matching `sesam.ErrRateLimited`.
