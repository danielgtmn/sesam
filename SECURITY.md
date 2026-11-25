# Security policy

sesam decides who is signed in to the apps that use it, so security reports are welcome and taken seriously.

## Reporting a vulnerability

**Please do not open a public issue.** Report it privately instead:

1. Go to [Security → Report a vulnerability](https://github.com/danielgtmn/sesam/security/advisories/new).
2. Describe what is affected, how to reproduce it (ideally a Go test), and what an attacker gains. Add the version from your `go.mod`.

You get an answer within a week. Once a fix is released, the advisory is published, and you are credited unless you prefer not to be.

## Supported versions

sesam is before 1.0. Fixes go into `main` and into the next release, which is cut promptly for security fixes. Only the **latest release** receives fixes.

## Scope

In scope: every package in this repository and the HTTP endpoints they serve.

Out of scope: vulnerabilities in Go, database drivers or other dependencies themselves (report them upstream; tell us if sesam uses them unsafely), and apps built with sesam, unless sesam's documented use leads to the problem.

How sesam protects passwords, sessions and tokens, and what the app must do itself, is described in [Security](https://github.com/danielgtmn/sesam/wiki/Security) in the wiki.
