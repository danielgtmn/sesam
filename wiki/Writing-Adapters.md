# Writing adapters

An adapter implements `adapter.Adapter` (package `github.com/danielgtmn/sesam/adapter`): `Schema`, `Migrate`, `Create`, `FindOne`, `FindMany`, `Update`, `Delete`, `Count` and `Tx`. It stores rows of declared tables and answers simple queries; it never needs to know what the rows mean.

Rules every adapter follows:

- Values are `string`, `int64`, `bool`, `time.Time` (UTC, microseconds) or nil. Call `adapter.CheckRow` and `adapter.CheckWhere`, which validate field names and normalize values, before touching the database.
- Unknown tables and fields are errors; never put a name into a query that was not declared.
- Unique violations (field, index, primary key) are `adapter.ErrConflict`; `FindOne` without a match is `adapter.ErrNotFound`. A nil value never conflicts.
- `Eq`/`Ne` with nil test for NULL; `Ne` with a value does not match NULL. `Prefix` is case-sensitive and has no wildcards. nil sorts first ascending.
- `Incr` is atomic: concurrent increments are never lost.
- `Tx` runs everything or nothing; `Tx` inside `Tx` joins it.
- `Migrate` only adds and can run any number of times.

Prove it with the conformance tests:

```go
func TestConformance(t *testing.T) {
    adaptertest.Run(t, func(t *testing.T) adapter.Adapter { return newAdapterOnEmptyDatabase(t) })
}
```

If your adapter can open a second instance on the same database, implement `adaptertest.Reopener` (in a test wrapper) to run the migration test, too.
