# sesam

sesam is authentication you embed in a Go app. Instead of running a separate sign-in service, you add sesam to your program: it keeps users and sessions in your database, serves the sign-in endpoints under a path of your choice, and tells your handlers who is signed in.

It is built like [Better Auth](https://www.better-auth.com): a small core, every way of signing in as a plugin, and adapters for the database.

What it does today:

- **Users** with an e-mail address, a username, or both.
- **Sign-in with a password**, with attempt limits, password change and reset by e-mail link.
- **Sessions** in a secure cookie, ending after a time without use and after a maximum lifetime. People can see and end their sessions.
- **SQLite and Postgres**, through the driver your app already uses.
- **Hooks** to react to or refuse sign-ups, sign-ins and deletions.

sesam answers "who is this?". What that person may do is decided by your app.

## Status

sesam is **early**. What exists is tested on every supported database, but the API may change before 1.0. See [[Roadmap|Roadmap]] for what comes next.

## Pages

- [[Getting Started|Getting-Started]]: add sesam to an app
- [[Configuration|Configuration]]: every option of the core
- [[Password|Password]]: the password plugin
- [[HTTP API|HTTP-API]]: the endpoints
- [[Databases|Databases]]: SQLite, Postgres, tables and migrations
- [[Writing Plugins|Writing-Plugins]] and [[Writing Adapters|Writing-Adapters]]
- [[Security|Security]]: what sesam protects and what is up to you
- [[Architecture|Architecture]]: how the parts fit together
- [[Development|Development]]: build, test, release

The source is at <https://github.com/danielgtmn/sesam>.
