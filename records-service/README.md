# Records service: a full-stack request pipeline (Go, React, SQLite)

**Requirement this addresses:** reliable transaction processing and data integrity across integrated systems, with a complete audit trail, relationship-based access control, and an accessible operator interface conforming to Section 508 and WCAG 2.1 AA.

A public records office, end to end. A citizen's request is filed, acknowledged, assigned,
released, delivered, and closed; a second service assembles and delivers the release package and
reports what happened; a records officer works the whole thing from a console that can be operated
without a mouse. The interesting part is not the happy path — it is what the system does when two
officers decide at once, when the same command arrives twice, when the machine loses power between
recording a decision and announcing it, and when the package cannot be delivered after the records
have already been released.

This example builds on [`../event-sourced-aggregate`](../event-sourced-aggregate). The aggregate
there is the domain core here, carried across by copy so this directory stays self-contained and
extended with the release-and-delivery leg that gives the service a transaction spanning two
services. The authorization model follows the method in
[`../rebac-authorization`](../rebac-authorization); the accessibility gate follows
[`../accessibility-gated-ci`](../accessibility-gated-ci); the port-and-adapter shape and its shared
contract follow [`../hexagonal-service`](../hexagonal-service).

## What it demonstrates

- **One stream of record, three consumers.** Persistence is an event store, not a state table:
  append events at an expected version, rebuild the aggregate by replay. Everything else reads that
  one stream — rehydration for writes, a projection for the console's list, and the outbox relay for
  the messages other services receive. Because all three read the same events, they cannot disagree
  about what happened, and the read model can be deleted and rebuilt with no migration.
- **Optimistic locking enforced by the database.** The lock is `UNIQUE (request_id, version)`, not
  a check in application code that a later refactor could move outside the transaction. A writer
  that read version 3 cannot write version 4 twice, and the loser is told `409` with the version to
  start again from — never a silent overwrite.
- **Idempotency at two levels.** Every command carries a key. A cheap pre-check returns the
  original result so a resubmitted command is not refused by an invariant it already satisfied; the
  authoritative check is a primary key inside the transaction, so eight simultaneous retries produce
  one event.
- **A transactional outbox, and a relay that can die anywhere.** The outbox entry is committed with
  the event it announces, so no fact is recorded without its announcement and no announcement
  escapes for a fact that was rolled back. The relay publishes and *then* marks; a crash between the
  two republishes on restart, which is why every message id is derived from the fact and every
  consumer deduplicates on it.
- **CloudEvents 1.0 with W3C Trace Context.** The traceparent is recorded with the event and
  carried as the CloudEvents distributed-tracing extension attribute, so a message published a
  minute later still belongs to the trace of the request that caused it. The consumer continues that
  trace rather than starting a new one.
- **A distributed transaction by choreography.** The fulfillment service consumes the release,
  assembles a package idempotently, and emits either a delivery confirmation or a compensating
  failure. The records service applies the fact to its own aggregate: confirmation closes the
  request, failure withdraws the release and puts it back in front of the officer. Coordination is
  event choreography with compensating events. There is no coordinator process and no workflow
  engine.
- **Relationship-based authorization at every entry point.** A check runs before every command and
  every read, against a model that says a requester may read their own request, office staff may
  read the office's requests, a clerk may send the receipt notice, a records officer decides the
  answer, and only the office's fulfillment service may report a delivery outcome — so an officer
  cannot mark their own release delivered.
- **A console with no rules of its own.** Every visual state is derived from the aggregate's single
  `status` field; every control is rendered from the `allowed_actions` the server returned. Change
  the authorization model and the screen changes without a front-end release.
- **Section 508 conformance as a build gate.** Landmarks, headings, labels, a full keyboard path
  through the whole flow, focus trapped in the panel and returned on close, live-region
  announcements for everything that happens without the operator asking, AA contrast in light and
  dark, and motion that respects `prefers-reduced-motion`.

## Layout

```
records-service/
├── recordsrequest/        the domain core: events, commands, aggregate, codec, and the ports
├── memorystore/           driven adapter: the in-memory twin of the event store and read model
├── sqlitestore/           driven adapter: the same ports on SQLite (pure Go, no cgo)
├── storetest/             the executable contract both store adapters run
├── projector/             the read model's catch-up loop over the stream
├── outbox/                message construction from stored events, and the relay
├── cloudevent/            CloudEvents 1.0 envelopes and W3C Trace Context
├── bus/                   the in-process transport between the two services
├── delivery/              the message contract the two services share
├── fulfillment/           the second service: assembles packages, reports outcomes
├── authz/                 the authorization port and its in-memory adapter
├── openfgaauthz/          the same port answered by OpenFGA, with model.fga
├── authztest/             the executable contract both authorization adapters run
├── requests/              the application service, and the delivery-outcome consumer
├── choreography/          the wiring: three subscriptions and a relay
├── httpapi/               the HTTP adapter, RFC 7807 problems, and 409 with the current version
├── cmd/server/            runs everything in one process and serves the console
├── features/              the acceptance criteria, in Gherkin
├── acceptance/            the godog step definitions for those criteria
└── web/                   the console: React, TypeScript, Vite, Vitest, Playwright, axe
```

## The tests are the specification

Testing here is Gherkin-driven: the acceptance criteria are written first, in the office's
vocabulary, and they are what runs. `go test ./...` executes every scenario in `features/` through
godog as ordinary Go subtests — there is no separate runner and no separate command, so a criterion
that stops holding breaks the build the same way a unit test does. Behavior beyond the criteria is
covered by table-driven unit tests and by two executable contracts that every adapter runs.

| Feature file | What its scenarios prove |
| --- | --- |
| [`records_request_lifecycle.feature`](features/records_request_lifecycle.feature) | Each step is possible only after the step before it. Work before acknowledgment, release without an accountable officer, denial without a citation, and any command after closing are all refused, and a refused command leaves no event. |
| [`authorization.feature`](features/authorization.feature) | Allow *and* deny for every action, by relationship: a clerk takes work in but cannot decide it; an officer of another office reaches nothing; a requester reads their own request and nothing else; only the fulfillment service may report a delivery outcome, so an officer cannot compensate their own release. The list a caller sees holds only what they may read. |
| [`idempotent_resubmission.feature`](features/idempotent_resubmission.feature) | The same command twice is one event. A retried filing creates one request and queues one message; a retried acknowledgment returns the original result instead of failing on "already acknowledged"; eight simultaneous copies apply once; the same command under a *new* key is correctly a second command. |
| [`optimistic_locking.feature`](features/optimistic_locking.feature) | Two officers deciding from the same version: one wins, the other is told the record changed, and nothing of the loser's is written — not the event, not the message. Eight at once still produce one decision. Reading again and deciding again succeeds. |
| [`transactional_outbox.feature`](features/transactional_outbox.feature) | The message is committed with the fact. A refused change queues nothing. A process that dies before the relay runs loses nothing. A process that dies *between* publishing and recording the dispatch republishes on restart — the message goes out twice, and exactly one package is assembled. |
| [`release_choreography.feature`](features/release_choreography.feature) | The happy path and the compensation path, end to end across both services: delivery closes the request; a permanent assembly failure withdraws the release, restores the page count to zero, and records the reason for the officer, who can then release again or deny. The delivery outcome belongs to the trace the release started, in a different span. A *temporary* failure is retried rather than compensated. |
| [`event_stream_replay.feature`](features/event_stream_replay.feature) | The stream is the record: a request rebuilt from its events alone matches the one the writer left; the read model can be discarded and rebuilt from the stream with identical rows; every event carries the trace and the idempotency key of the command that caused it. |
| [`console_conflict_ux.feature`](features/console_conflict_ux.feature) | The exact HTTP exchanges the console makes: the entity tag is the version, the response carries the actions this caller may take, a stale edit is `409` with `current_version`, a domain refusal is `422` and an authorization refusal is `403` — three different answers that must not be confused for one another. |

Beyond the criteria:

| Suite | What it proves |
| --- | --- |
| [`storetest`](storetest/contract.go) | Twenty cases every store adapter must pass, including replay equivalence, metadata recording, the interleaved-writer race, the double-submit race, outbox ordering, and projection idempotency by sequence. The in-memory twin is a legitimate test double for exactly one reason: it passes this. |
| [`authztest`](authztest/contract.go) | An allow-and-deny matrix every authorization adapter must pass. The in-memory evaluator and a live OpenFGA answering `model.fga` return the same decision for all of it. |
| [`cloudevent`](cloudevent) | Traceparent parsing at the edges — the reserved version, all-zero identifiers, uppercase hex, trailing data, and a later specification version that must still be accepted — and the envelope wire format, attribute by attribute. |
| [`recordsrequest`](recordsrequest) | Command invariants as a table, replay equivalence, and a codec round trip for every event: a field that does not round-trip is a fact the agency could not produce later. |
| [`web/src/derive.test.ts`](web/src/derive.test.ts) | The console's whole state model: the status table, and the control set for every combination of status and server-granted capability, including the clerk who is offered no release control. |
| [`web/src/App.test.tsx`](web/src/App.test.tsx) | Skeleton to data with the announcement that replaces it; the panel taking focus and giving it back; a `409` producing a visible conflict that names the version and saves nothing; a `422` attaching its message to the field it was about. |
| [`web/tests/e2e`](web/tests/e2e) | axe against the real service in five states — list, panel open, form error, conflict, and dark mode — plus the whole flow driven by keyboard alone, and a check that focus cannot leave the open panel in either direction. |

## Run it

```bash
cd records-service

# The back end: unit tests, the executable contracts, and every acceptance criterion.
go test ./... -race

# The same suite against a live OpenFGA rather than the in-memory evaluator.
docker run -d --name openfga -p 8080:8080 openfga/openfga:v1.8.4 run
OPENFGA_API_URL=http://localhost:8080 go test ./openfgaauthz/... -count=1

# The console's unit and component tests.
cd web && npm install && npm test

# The browser gate: builds the console, starts the real service, runs axe and the keyboard path.
npx playwright install --with-deps chromium
npm run e2e
```

To use it rather than test it:

```bash
cd records-service/web && npm install && npm run build
cd .. && go run ./cmd/server -web ./web/dist        # then open http://127.0.0.1:8081
```

Pass `-db records.db` to keep the events on disk instead of in memory; the same contract holds
either way. The console's operator picker switches between a clerk and a records officer, which is
the quickest way to see the controls change with the authorization model. Releasing more than 200
pages makes the courier refuse the package, which shows the compensation path from the screen.

Go 1.24 or newer; Node 22 or newer for the console.

## Section 508 and WCAG 2.1 AA

What the build checks mechanically, on every push:

- axe-core against the WCAG 2.0/2.1 A and AA tag sets plus axe's structural best-practice rules, in
  five states — the list, the open panel, a form that was refused, a conflict, and the whole thing
  in dark mode. Contrast is checked against computed colors, so a palette that passes in one theme
  and not the other fails.
- The full operator flow driven by keyboard alone: skip link, list, open the panel, edit, submit,
  hit a conflict, recover from it, decide again, close, and confirm focus returns to the control
  that opened the panel.
- Focus containment in both directions while the panel is open, which is the half usually left out.
- Errors programmatically associated with their fields (`aria-invalid` plus `aria-describedby`), and
  a live region carrying every change the operator did not ask for.

What a formal audit still covers, and this build does not:

- Screen reader behavior in the readers agencies actually use. Automated checks read the
  accessibility tree; they do not tell you whether a screen makes sense when it is read aloud.
- Magnification and reflow at 400 percent, and text spacing overrides.
- Cognitive load, plain-language review of the wording, and whether error messages tell somebody
  what to do rather than what went wrong.
- Voice control and switch access, which depend on names and targets that automated rules only
  partly constrain.
- A VPAT covering the delivered product rather than this example.

Automated coverage of this kind typically catches a minority of real barriers. It is here because it
never regresses and never needs remembering, not because it is the whole of conformance.

## Notes

Identity arrives as a request header, stamped by an authorization sidecar in front of the service —
see [`../authorization-sidecar`](../authorization-sidecar). This service implements no login and
holds no credential. The console's operator picker exists so the example can be operated without an
identity provider; in a deployment a browser cannot set that header, and the service refuses any
request that arrives without it.

The in-process message bus is a test decision, not an architectural one: a broker sits in its place
in a deployment, and nothing above the `outbox.Publisher` port knows which it is talking to.
Delivering synchronously is what lets the acceptance criteria assert an outcome without sleeping,
and a test that sleeps is a test that will be flaky on a loaded build machine.

`model.json` is generated from `model.fga`, which is the source a reviewer reads:

```bash
fga model transform --file openfgaauthz/model.fga | jq . > openfgaauthz/model.json
```

All names, offices, requesters, and case data here are synthetic.
