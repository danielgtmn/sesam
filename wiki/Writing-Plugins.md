# Writing plugins

A plugin is a Go value that implements `sesam.Plugin`:

```go
type Plugin interface {
    ID() string                 // unique, e.g. "twofactor"
    Schema() []schema.Table     // tables it needs, or fields it adds to existing ones
    Init(a *sesam.Auth) error   // keep a, register hooks
}
// optional:
type RoutesPlugin interface {
    Plugin
    Routes(r *sesam.Router)     // r.Handle("POST", "/two-factor/verify", h)
}
```

The plugin value is its own Go API (`pw.SignIn(…)`), so apps get typed methods. Put it under `plugins/<name>`.

## Declaring data

```go
func (p *Plugin) Schema() []schema.Table {
    return []schema.Table{
        {Name: "two_factor", Fields: []schema.Field{
            {Name: "user_id", Type: schema.String, Required: true, Ref: &schema.Reference{Table: sesam.TableUsers}},
            {Name: "secret", Type: schema.String, Required: true},
            {Name: "enabled_at", Type: schema.Time},
        }},
        // adds a field to the users table
        {Name: sesam.TableUsers, Fields: []schema.Field{{Name: "two_factor", Type: schema.Bool}}},
    }
}
```

Every table has an implicit `id` (text). Types are `String`, `Int`, `Bool` and `Time`. Fields added to an existing table must not be `Required` (existing rows have no value). A field with `Ref` to `users` makes `DeleteUser` delete the rows with the user.

## Using data

```go
db := a.Adapter()
row, err := db.FindOne(ctx, "two_factor", adapter.Eq("user_id", u.ID))   // adapter.ErrNotFound
err = db.Create(ctx, "two_factor", adapter.Row{"id": sesam.NewID(), "user_id": u.ID, "secret": s})
n, err := db.Update(ctx, "two_factor", adapter.Row{"enabled_at": a.Now()}, adapter.Eq("user_id", u.ID))
err = db.Tx(ctx, func(tx adapter.Adapter) error { … }) // use tx inside, return on the first error
```

Conditions: `Eq`, `Ne` (nil tests for NULL), `Lt`, `Lte`, `Gt`, `Gte`, `In`, `Prefix`. `adapter.Incr(n)` in `Update` adds atomically. Unique violations are `adapter.ErrConflict`.

## What the core offers plugins

| | |
|---|---|
| `a.SignIn(ctx, u, meta)` | Runs the sign-in hooks and creates the session; call it once the person proved who they are. |
| `a.CreateUserTx(ctx, nu, fn)` | Creates a user and runs `fn` in the same transaction (e.g. to add an account). |
| `a.Limit(ctx, key, max, window)` / `a.UndoLimit` / `a.ResetLimit` | Stored attempt limits. `UndoLimit` takes back one attempt (a success that must not count), `ResetLimit` forgets them all. |
| `a.CreateVerification` / `a.ConsumeVerification` | One-time tokens that work exactly once. |
| `a.AddHooks(sesam.Hooks{…})` | E.g. `BeforeSignIn` to demand a second step. |
| `sesam.NewID`, `NewToken`, `HashToken` | Random ids, secrets, and the hash to store instead of a secret. |
| `sesam.ReadJSON`, `WriteJSON`, `a.SessionFromRequest`, `a.SetSessionCookie`, `a.Meta` | For endpoints. |

Return `*sesam.Error` (or `sesam.InvalidInput`) for anything the person should read. Plugins never import other plugins.
