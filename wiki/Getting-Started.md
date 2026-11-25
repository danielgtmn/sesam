# Getting started

```sh
go get github.com/danielgtmn/sesam
```

## A complete app

`examples/basic` in the repository is this program; run it with `go run ./examples/basic`.

```go
db, err := sql.Open("sqlite", "app.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_txlock=immediate")
store, err := sqladapter.New(db, sqladapter.SQLite)

pw := password.New(password.Options{
    // optional, with SendEmail: enables "forgot password"
    ResetURL: func(token string) string { return "https://app.example.com/reset?token=" + token },
})
auth, err := sesam.New(sesam.Config{
    Adapter:   store,
    BaseURL:   "https://app.example.com",
    SendEmail: sendWithMyMail, // func(ctx, sesam.Email) error; sesam never sends mail itself
    Plugins:   []sesam.Plugin{pw},
})
if err := auth.Migrate(ctx); err != nil { … } // creates the auth_* tables

mux := http.NewServeMux()
mux.Handle("/api/auth/", auth.Handler())
mux.Handle("GET /me", auth.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    cur, _ := sesam.FromContext(r.Context())
    sesam.WriteJSON(w, 200, cur.User)
})))
http.ListenAndServe(":8080", auth.Middleware(mux))
```

1. **Open your database** with your driver and wrap it in an adapter ([[Databases|Databases]]).
2. **Create the plugins** you want, here the [[Password|Password]] plugin.
3. **Create the `Auth`** with `sesam.New`. `BaseURL` is where your app is reached; it decides which pages may send requests and whether cookies are `Secure` ([[Configuration|Configuration]]).
4. **Migrate** at every start. It only adds what is missing.
5. **Mount the handler** at `/api/auth/` ([[HTTP API|HTTP-API]]) and wrap your app in `Middleware`, which finds the signed-in user for `sesam.FromContext`.
6. **Clean up** now and then with `auth.DeleteExpired(ctx)`, e.g. once an hour.

## Try it

```sh
go run ./examples/basic &
curl -c jar -H 'Content-Type: application/json' \
  -d '{"email":"anna@example.com","password":"a long password"}' \
  localhost:8080/api/auth/sign-up
curl -b jar localhost:8080/me
```

## Without the endpoints

Everything the endpoints do is available as Go methods, so an app can keep its own handlers and UI:

```go
u, token, session, err := pw.SignIn(ctx, password.Credentials{Identifier: name, Password: p}, auth.Meta(r))
if err != nil { … }               // *sesam.Error: show err.Error() to the person
auth.SetSessionCookie(w, token)

s, u, err := auth.Session(ctx, auth.SessionToken(r))  // who is this?
err = auth.SignOut(ctx, auth.SessionToken(r))
```

Admins create users with `pw.CreateUser(ctx, sesam.NewUser{…}, password)`, which works even when sign-up is turned off.
