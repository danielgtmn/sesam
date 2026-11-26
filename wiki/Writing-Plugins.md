# Writing plugins

A plugin is a Go value that implements `sesam.Plugin`:

```go
type Plugin interface {
    ID() string                 // unique, e.g. "twofactor"
    Init(a *sesam.Auth) error   // keep a, declare tables, register hooks
}
// optional:
type RoutesPlugin interface {
    Plugin
    Routes(r *sesam.Router)     // r.Handle("POST", "/two-factor/verify", h)
}
```

The plugin value is its own Go API (`pw.SignIn(…)`), so apps get typed methods. Put it under `plugins/<name>`.

## Declaring data

Plugins keep their data with [ablage](https://github.com/danielgtmn/ablage) on the app's store (`a.Store()`). Declare the tables in `Init`, usually as a typed repository; `auth.Migrate` creates them.

```go
type secret struct {
    ID        string
    UserID    string     `db:",ref=auth_users,cascade,unique"`
    Secret    string
    EnabledAt *time.Time // pointers are nullable
}

func (p *Plugin) Init(a *sesam.Auth) (err error) {
    p.a = a
    p.secrets, err = ablage.NewRepository[secret](a.Store(), "auth_two_factor")
    return err
}
```

- Name tables `auth_<plugin>_…`.
- Reference users with `ref=auth_users,cascade`, so `DeleteUser` removes the plugin's rows with the user (in every store).
- Plugins cannot add fields to sesam's tables; keep such data in a table of the plugin.
- Tables only grow: a new field of an existing table gets its zero value in old rows (or NULL if it is a pointer). Never rename a field; keep the old column name with a `db` tag.

See ablage's wiki for the struct mapping, conditions (`ablage.Eq`, `And`, `In`, `Prefix`, …) and errors (`ablage.ErrNotFound`, `ablage.ErrDuplicate`).

## Using data

```go
s, err := p.secrets.First(ctx, ablage.Query{Where: ablage.Eq("user_id", u.ID)}) // ablage.ErrNotFound
err = p.secrets.Create(ctx, &secret{UserID: u.ID, Secret: x})                   // ablage.ErrDuplicate
err = p.secrets.Patch(ctx, s.ID, ablage.Row{"enabled_at": a.Now()})
err = a.Store().Tx(ctx, func(tx ablage.Store) error {                           // use tx inside, never the outer store
    return p.secrets.With(tx).Delete(ctx, s.ID)
})
```

## What the core offers plugins

| | |
|---|---|
| `a.SignIn(ctx, u, meta)` | Runs the sign-in hooks and creates the session; call it once the person proved who they are. |
| `a.CreateUserTx(ctx, nu, fn)` | Creates a user and runs `fn(tx, u)` in the same transaction (e.g. to add an account with `a.Accounts().With(tx)`). |
| `a.Accounts()` | The repository of `sesam.Account`: ways to sign in, e.g. the password hash (provider `credential`). |
| `a.Store()` | The store, for the plugin's own tables. |
| `a.Limit(ctx, key, max, window)` / `a.UndoLimit` / `a.ResetLimit` | Stored attempt limits. `UndoLimit` takes back one attempt (a success that must not count), `ResetLimit` forgets them all. |
| `a.CreateVerification` / `a.ConsumeVerification` | One-time tokens that work exactly once. |
| `a.AddHooks(sesam.Hooks{…})` | E.g. `BeforeSignIn` to demand a second step. |
| `sesam.NewID`, `NewToken`, `HashToken` | Random ids, secrets, and the hash to store instead of a secret. |
| `sesam.ReadJSON`, `WriteJSON`, `a.SessionFromRequest`, `a.SetSessionCookie`, `a.Meta` | For endpoints. |

Return `*sesam.Error` (or `sesam.InvalidInput`) for anything the person should read. Plugins never import other plugins.
