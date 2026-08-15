# Hexagonal service (Go)

**Requirement this addresses:** avoidance of vendor lock-in and data portability; business logic independent of any specific database product.

A construction permit register built as ports and adapters. The domain declares the storage
interface it needs, in its own vocabulary; two adapters implement it — an in-memory register and
a SQLite one — and a single shared contract test holds both to the same promises. The application
service, and every use case in it, is tested against the in-memory adapter with no database
running anywhere.

## What it demonstrates

- **The port belongs to the domain.** `permit.Repository` is declared in `permit/repository.go`,
  next to the rules that need it, and it is written in register terms: register a permit, update
  one, look one up by number, list the ones whose term is running out. There is no row, no column
  and no query language in the interface. The `permit` package imports nothing but the standard
  library — in particular, no driver.
- **The twin is a real implementation, not a mock.** `memorystore` enforces uniqueness, stamps
  versions, rejects stale updates and orders results. It is not a stub that records calls; it is a
  register that behaves like one.
- **One contract, both adapters.** `repotest.RunRepositoryContract` is the executable definition of
  the port: not found, duplicate number, the lost-update conflict, ordering, exclusion of suspended
  permits, empty results. `memorystore` and `sqlitestore` each call it with one line. That is the
  whole argument for trusting the twin — it is held to the promises the real store is held to, so a
  test that passes against it is not passing for the wrong reason.
- **Swapping the store is a wiring change.** `register.New` takes the interface. Nothing in
  `permit` or `register` names SQLite; the only file that knows SQL exists is
  `sqlitestore/sqlitestore.go`. A different store is a new file in a new package that passes the
  same contract.
- **Optimistic concurrency is in the port, not the adapter.** Two officers reading the same permit
  and both acting on it is a domain event, not a database detail. `Permit.Version` is stamped by
  whichever adapter is installed, and a stale write is refused with `permit.ErrVersionConflict`
  identically by both — SQLite does it with a version in the `WHERE` clause, the map does it with a
  comparison, and no caller can tell.
- **Use cases without infrastructure.** `register/service_test.go` drives issue, suspend, renew and
  the renewal notice run end to end. It starts no database, opens no file and touches no network.
  The clock is injected for the same reason the register is, so "expiring within 30 days" is a
  fixed assertion rather than one that changes at midnight.

## Layout

| File | Contents |
| --- | --- |
| `permit/permit.go` | The entity and its transitions: issue, suspend, reinstate, renew. |
| `permit/repository.go` | The driven port. The domain's storage interface, in domain terms. |
| `permit/errors.go` | Sentinel errors — domain rules and port failures, kept apart. |
| `permit/permit_test.go` | Table-driven invariant tests for issue and every transition. |
| `register/service.go` | The application service: the use cases, depending only on the port. |
| `register/service_test.go` | Those use cases exercised against the in-memory twin, no database. |
| `repotest/contract.go` | `RunRepositoryContract` — the shared, executable definition of the port. |
| `memorystore/memorystore.go` | Driven adapter: the register in a map, concurrency-safe. |
| `memorystore/memorystore_test.go` | Runs the contract against the twin. |
| `sqlitestore/sqlitestore.go` | Driven adapter: the register in SQLite, via `database/sql`. |
| `sqlitestore/sqlitestore_test.go` | Runs the same contract against a real SQLite database. |

## Run it

```bash
cd hexagonal-service
go test ./... -v
```

Both adapters run the identical suite, so the output reads as the same ten contract cases twice:

```bash
go test ./memorystore/... ./sqlitestore/... -run TestContract -v
```

The SQLite driver is `modernc.org/sqlite`, a pure-Go translation of SQLite. There is no cgo, no
build tag and no service to start, so this works with `CGO_ENABLED=0` on a machine with nothing
installed but Go:

```bash
CGO_ENABLED=0 go test ./... -count=1
```

To see that the contract has teeth, delete the version check in `memorystore.Update` and run the
tests again: the twin stops passing the contract that the SQLite adapter still passes, which is
exactly the failure a silently-diverging test double would otherwise hide.

Go 1.24 or newer.

## Notes

All permit numbers, holders and addresses here are synthetic.

The sibling example [`../event-sourced-aggregate`](../event-sourced-aggregate) deliberately holds
no persistence code at all: it hands the caller `PendingEvents()` at a known `Version()` and stops.
This example is where that boundary goes. The same reasoning applies to either shape — the
aggregate would be stored through a port declared in its own package, with an in-memory event store
and a real one held to a shared append-at-expected-version contract. Different domain, so the two
examples do not restate each other, but one storage argument.

Two things this example is not. It ships no HTTP handler or message consumer: those are driving
adapters, and the boundary worth showing is the one on the driven side. And `permit.Permit` has
exported fields, because an adapter has to read and write every one of them; the invariants are
defended at the constructor and the transition methods, which return new values rather than
mutating in place, so a rejected transition cannot leave a half-changed permit for a caller to
store.
