# Upgrading to v2

sesam v2 keeps its data with [ablage](https://github.com/danielgtmn/ablage) instead of its own adapter packages. **Your database stays as it is**: v2 reads and writes the `auth_*` tables sesam 1.x created, on SQLite and Postgres. Users stay signed in, passwords, attempt counters and reset links carry over. A test in the repository (`internal/v1compat`) creates databases with sesam v1.0.0 and goes on with v2.

## What to change in your code

1. **Module path.** v2 is a new major version:

   ```sh
   go get github.com/danielgtmn/sesam/v2@latest github.com/danielgtmn/ablage@latest
   ```

   and replace `github.com/danielgtmn/sesam` by `github.com/danielgtmn/sesam/v2` in your imports (`…/v2/plugins/password`).

2. **The store instead of the adapter.**

   | v1 | v2 |
   |---|---|
   | `sqladapter.New(db, sqladapter.SQLite)` | `sqlstore.New(db, sqlstore.SQLite)` (`github.com/danielgtmn/ablage/sqlstore`) |
   | `sqladapter.New(db, sqladapter.Postgres)` | `sqlstore.New(db, sqlstore.Postgres)` |
   | `memadapter.New()` | `memstore.New()` (`github.com/danielgtmn/ablage/memstore`) |
   | `sesam.Config{Adapter: …}` | `sesam.Config{Store: …}` |
   | `auth.Adapter()` | `auth.Store()` |
   | `adapter.Row`, `adapter.Eq`, `adapter.ErrNotFound`, `adapter.ErrConflict`, `adapter.NormalizeTime` | `ablage.Row`, `ablage.Eq`, `ablage.ErrNotFound`, `ablage.ErrDuplicate`, `ablage.NormalizeTime` |
   | `adaptertest`, `schema` | gone; see ablage's `storetest` and `ablage.Table` |

   **Table prefix:** v1 put `auth_` in front by default. In v2 the names include it (`sesam.TableUsers == "auth_users"`), so create the store **without** `sqlstore.WithPrefix` to keep using your tables. If you used v1 with another prefix (`Options{TablePrefix: …}` or `NoPrefix`), rename the tables to `auth_users`, `auth_sessions`, `auth_accounts`, `auth_verifications` and `auth_rate_limits` before the first start.

   **SQLite foreign keys** must be on (`?_pragma=foreign_keys(1)` with `modernc.org/sqlite`); `sqlstore.New` refuses a database without them.

3. **Code that wrote sessions directly** (e.g. taking over sessions of an older sign-in): use `auth.ImportSession(ctx, sesam.Session{ID: …, UserID: …, CreatedAt: …, ExpiresAt: …, LastSeenAt: …})`. A session already there is an error matching `ablage.ErrDuplicate`.

4. **Plugins** no longer have `Schema()`. They declare their tables in `Init`, usually by creating repositories on `a.Store()`, and can no longer add fields to sesam's tables (keep such data in a table of the plugin). `CreateUserTx` passes an `ablage.Store`; password accounts are `sesam.Account` in `a.Accounts()`. See [[Writing Plugins|Writing-Plugins]].

5. **`auth.Tables()`** is gone.

## What happens to the database

At the first `Migrate`, nothing changes in the data. The indexes v1 created (`auth_users_email_key`, `auth_sessions_user_id_idx`, …) are replaced by identical ones named the ablage way (`auth_users_email_<hash>_key`); uniqueness and lookups stay the same. Indexes you added yourself are left alone.

On Postgres, v1 created text columns with the database's collation; ablage compares and sorts them byte by byte all the same, so results do not depend on the server's locale.

Order at start: run your own migrations, then `auth.Migrate`, as before.
