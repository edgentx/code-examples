# Event-sourced aggregate (Go)

**Requirement this addresses:** a complete, attributable audit trail of every change to a record, reconstructible for audit and records retention.

A public records request modeled as an event-sourced aggregate. Commands are validated against
current state; accepted commands produce events; state changes only by applying an event. The
event stream is the system of record, so the full history of a case is the storage format rather
than an audit log bolted on beside it.

## What it demonstrates

- **Command, invariant, event.** Every state change goes through a command method that either
  rejects the command with a named error or raises exactly one event. A rejected command mutates
  nothing and does not move the aggregate version.
- **Invariants in one place.** The rules an agency has to defend on appeal — a request must be
  acknowledged before it is worked, a closed request accepts nothing further, a denial must cite
  an exemption, a response must have an accountable reviewer — live in the command guards, not
  scattered across handlers.
- **Replay.** `FromHistory` rebuilds the aggregate from stored events alone. `replay_test.go`
  drives a request through its whole lifecycle by command, rebuilds it from the resulting events,
  and asserts the two are indistinguishable. That test is what makes the event stream trustworthy
  as the record: any read model can be dropped and rebuilt from it.
- **Replay applies no rules.** `apply` contains no validation. A rule added today must never make
  yesterday's history unloadable, and an event the current code does not recognize fails loudly
  (`ErrUnknownEvent`) instead of being silently skipped into a plausible-looking wrong state.
- **Optimistic concurrency.** `Version()` is the applied-event count, which is what an event store
  append is conditioned on.

## Layout

| File | Contents |
| --- | --- |
| `events.go` | The facts. Stable `EventName()` values are part of the storage contract. |
| `commands.go` | The requests to change state. Never stored. |
| `aggregate.go` | State, command guards, and the single `apply` mutation point. |
| `errors.go` | Sentinel errors, one per invariant, so callers branch on the rule. |
| `aggregate_test.go` | Table-driven invariant tests: each case is a history, a command, and the expected rule. |
| `replay_test.go` | Lifecycle-versus-replay equivalence, and the unknown-event failure. |

## Run it

```bash
cd event-sourced-aggregate
go test ./... -v
```

No dependencies beyond the Go standard library; Go 1.24 or newer.

## Notes

The aggregate holds no persistence code on purpose. Storage is a separate concern: an event store
appends `PendingEvents()` at the expected `Version()` and the aggregate is told
`MarkCommitted()`. See [`../hexagonal-service`](../hexagonal-service) for how that boundary is
drawn as a port with interchangeable adapters.

All names and case data here are synthetic.
