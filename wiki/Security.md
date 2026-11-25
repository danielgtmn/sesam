# Security

What sesam does, and what remains the app's job.

## Passwords

- Hashed with **argon2id** (64 MiB, 2 passes, 2 lanes, 16-byte salt). At most `HashConcurrency` hashes run at once, so a flood of sign-ins cannot exhaust memory.
- Never stored, logged or returned. Overlong inputs are refused before hashing.
- **Unknown names and wrong passwords** give the same answer and take the same time (a hash is checked either way).
- **Attempt limits**: 5 failures per name and 20 attempts per client address within 15 minutes by default. They are stored, so they survive restarts and apply across all instances.

## Sessions

- The cookie holds 32 random bytes. The database holds only their SHA-256, so a copy of the database does not let anyone sign in.
- The cookie is `HttpOnly`, `SameSite=Lax` by default, `Path=/`, and with https `Secure` with the `__Host-` prefix (no `Domain`, so subdomains cannot set or read it).
- Sessions end after an idle time (3 days) and a maximum lifetime (30 days). Changing a password ends the other sessions; setting or resetting it ends all of them.

## Requests from other sites

Changing requests to the endpoints (and changing requests with a session cookie through `Middleware`) are refused when the browser says they come from another origin (`Origin`, or `Sec-Fetch-Site: cross-site`). With `RequireHeader`, they also need a custom header that other sites cannot set. Your own changing endpoints outside `Middleware` can use `auth.CheckRequest(r)`.

## One-time tokens

Reset links carry 32 random bytes; only the SHA-256 is stored. They expire (1 hour), work exactly once even when used twice at the same moment, and a new link replaces the old one. Reset requests answer the same for unknown addresses and are limited to 3 per address and hour.

## Up to the app

- **Client address behind a proxy:** set `Config.ClientIP`. Without it, all clients behind the proxy share one attempt limit. Trust a forwarded header only from your proxy.
- **https:** use an `https` `BaseURL` in production; otherwise cookies are not `Secure`.
- **Content Security Policy and XSS:** a script injected into your pages can act as the signed-in person. The cookie is `HttpOnly`, so it cannot be read.
- **What people may do:** sesam only says who someone is.
- **Sending mail:** reset links are secrets; send them only to the user's address, and do not log them in production.
- **Cleaning up:** call `DeleteExpired` regularly.

## Known gaps

- No second sign-in step yet (planned: two-factor plugin).
- E-mail addresses are not confirmed at sign-up yet; only a password reset marks them verified.
- No check of passwords against lists of leaked passwords.
