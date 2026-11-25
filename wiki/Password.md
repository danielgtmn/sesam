# Password

`github.com/danielgtmn/sesam/plugins/password` lets people sign in with an e-mail address or a username and a password.

```go
pw := password.New(password.Options{
    Identifier:    password.Username, // default password.Email
    DisableSignUp: true,              // only admins create accounts
})
```

## Options

| Option | Default | Meaning |
|---|---|---|
| `Identifier` | `Email` | What people sign in with: `password.Email` or `password.Username`. That field is required at sign-up; the other is optional. |
| `DisableSignUp` | false | Refuses `SignUp` and `POST /sign-up`. `CreateUser` still works. |
| `MinLength`, `MaxLength` | 10, 200 | Password length in characters. |
| `MaxAttemptsPerName` | 5 | Failed sign-ins for one name within `AttemptWindow`; then sign-in for that name is refused, from every address, until the window ends. |
| `MaxAttemptsPerClient` | 20 | Sign-in attempts from one client address within the window, for any names. |
| `AttemptWindow` | 15 minutes | |
| `Mailer`, `ResetURL` | none | Both together enable "forgot password". `ResetURL(token)` builds the link to your page, which then posts the token and the new password to `/reset-password`. |
| `ResetTTL` | 1 hour | How long a reset link works. |
| `HashConcurrency` | 4 | Passwords hashed at the same time (64 MiB each). |

```go
type Mailer interface {
    SendPasswordReset(ctx context.Context, u sesam.User, link string) error
}
```

## Go API

| Method | |
|---|---|
| `SignUp(ctx, SignUpInput, meta)` | Creates the account and signs in. |
| `CreateUser(ctx, NewUser, password)` | Creates an account without signing in (admins, first-run setup). |
| `SignIn(ctx, Credentials, meta)` | Returns the user, the cookie token and the session. |
| `ChangePassword(ctx, userID, current, new, keepSessionID)` | Checks the current password; ends all other sessions. |
| `SetPassword(ctx, userID, password)` | Without the old password (admins); ends all sessions. Gives a user without a password one. |
| `SetPasswordHash(ctx, userID, hash)` | Stores an existing argon2id hash, to move users from another system without resetting passwords. Hashes from gpanel work as they are. |
| `HasPassword(ctx, userID)` | |
| `RequestReset(ctx, email, meta)` / `ResetPassword(ctx, token, password)` | The "forgot password" flow. |
| `ValidatePassword(pw)` | The length check, for forms. |

`meta` is `auth.Meta(r)`: the client's address and browser, for the attempt limit and the session list. Without an address (empty `meta.IP`), only the per-name limit applies.

## Behaviour

- **Unknown names and wrong passwords** give the same error (`invalid_credentials`) and take the same time, so nobody can find out which accounts exist.
- **Successful sign-in** clears the failed attempts for that name.
- **Password change** ends all other sessions; **set and reset** end all sessions.
- **Reset requests** answer the same for known and unknown addresses. At most 3 mails per address and hour. Links work once, and a newer link replaces an older one. Using a link marks the e-mail address as verified.
- **Hashing** is argon2id (64 MiB, 2 passes, 2 lanes) in the format `$argon2id$v=19$m=65536,t=2,p=2$…`.
