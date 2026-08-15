# Dapr pub/sub between two services (Go)

**Requirement this addresses:** guaranteed message delivery between systems, with failed messages retried and dead-lettered for review rather than dropped.

Two small services and a topic. A records office posts a document intake notice to an HTTP API;
the API validates it and publishes it; a second service consumes it. The interesting part is not
the happy path — it is the three ways a delivery can end, the delivery budget that bounds how long
a bad message is retried, and the dead-letter topic that catches what the budget could not fix. A
side effect of at-least-once delivery is that every consumer needs an idempotency key, so the
CloudEvents envelope and its stable `id` are spelled out rather than left to a library.

## What it demonstrates

- **The delivery contract, exactly.** With Dapr's HTTP subscriber, every verdict is `200 OK` and
  the **body** decides the message's fate: an empty body acknowledges, `{"status":"RETRY"}` asks
  for redelivery, `{"status":"DROP"}` stops delivery. A non-2xx is also a retry, so a handler that
  panics into a 500 gets redelivery by accident rather than by decision — and a handler that
  swallows an error and falls out returning a bare 200 has acknowledged a message it never
  processed. `subscriber_test.go` drives each path through `httptest` and asserts the exact
  response bytes, because a stray newline changes the outcome and no compiler will tell you.
- **DROP does not mean discard when a dead-letter topic is configured.** Verified against
  daprd 1.15.5 and captured below: the sidecar logs the drop and forwards the message to the
  dead-letter topic. That is the behavior this example wants, and it is the opposite of what the
  word sounds like.
- **A bounded delivery budget, then park.** The subscriber cannot tell a transient failure from a
  permanent one, so it treats them identically: retry up to `MAX_DELIVERY_ATTEMPTS`, then publish
  the message to the dead-letter topic and acknowledge. If the park itself fails, it asks for
  redelivery instead of acknowledging — an unparkable message is never acknowledged away.
- **State with etag concurrency.** The attempt counter is read with its etag and written back
  conditionally with `first-write` concurrency. With the default `last-write` the etag is accepted
  and ignored, and a lost update looks exactly like a success. A 409 gets its own error so the
  handler can tell "somebody beat me to it" (retry) from "the store is broken" (also retry, but
  for a different reason and with a different log line).
- **Ports, so the tests need no sidecar.** `Publisher` and `Store` are two-method interfaces. The
  daprd HTTP adapters are tested against `httptest` stubs that assert the URL shape, the content
  type, and the request body; the handlers are tested against in-memory fakes. The whole suite
  runs with no Docker and no daprd.
- **The house event convention.** `specversion`, `type`, `source`, `id`, `time`,
  `datacontenttype`, `data`, and `traceparent`. Publishing bare JSON lets the sidecar build the
  envelope and mint the `id`; publishing `application/cloudevents+json` passes the producer's
  envelope through untouched. Idempotency depends on a stable event id, so the intake API derives
  `id` from the notice identifier and uses the second form. A publisher that retries with a fresh
  UUID has turned one intake notice into two.
- **Declarative subscriptions.** The routing table is a file an operator can read and diff, not a
  registration buried in application startup.

## Layout

| File | Contents |
| --- | --- |
| `cloudevents.go` | The envelope, and why `id` has to be stable. |
| `intake.go` | The synthetic agency payload, its validation, and the publishing API. |
| `publisher.go` | The `Publisher` port and its daprd HTTP adapter. |
| `state.go` | The `Store` port and its daprd HTTP adapter, including the 409 path. |
| `subscriber.go` | The delivery contract, the attempt budget, and the dead-letter handler. |
| `errors.go` | Sentinel errors, one per failure the handlers have to distinguish. |
| `cmd/publisher/`, `cmd/subscriber/` | Two `main` packages; configuration by environment. |
| `components/pubsub.yaml` | Redis Streams broker. No password in the file. |
| `components/statestore.yaml` | Redis key/value store, keys namespaced by app id. |
| `components/subscription.yaml` | Topic → route, with `deadLetterTopic`. |
| `components/subscription-deadletter.yaml` | The dead-letter topic's own subscriber. |
| `components/resiliency.yaml` | Bounds the sidecar's redelivery so the dead-letter topic engages. |
| `subscriber_test.go` | The contract table, the poison-message walk, the unparkable case. |
| `state_test.go`, `publisher_test.go` | The sidecar HTTP APIs, asserted request by request. |
| `smoke.sh` | Brings the stack up, runs the sequence below, asserts, tears down. |

## Run it

The tests need nothing but Go:

```bash
cd dapr-pubsub
go test ./... -count=1
```

The demo stack is five containers: Redis, two services, and a sidecar each.

```bash
docker compose up --build -d
```

Publish a notice the subscriber can process, one it will reject before the topic ever sees it, and
one it can never process:

```bash
curl -s -X POST http://localhost:18300/intake \
  -H 'Content-Type: application/json' \
  -H 'traceparent: 00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01' \
  -d '{"noticeId":"N-1001","agencyCode":"DPR","seriesCode":"RS-100","pageCount":12}'
# {"eventId":"N-1001","topic":"intake-notices"}

curl -s -X POST http://localhost:18300/intake \
  -H 'Content-Type: application/json' \
  -d '{"noticeId":"N-1002","agencyCode":"DPR","seriesCode":"","pageCount":12}'
# {"error":"required field is missing"}

curl -s -X POST http://localhost:18300/intake \
  -H 'Content-Type: application/json' \
  -d '{"noticeId":"N-9001","agencyCode":"DPR","seriesCode":"RS-999","pageCount":40}'
# {"eventId":"N-9001","topic":"intake-notices"}
```

Then publish an event this consumer does not own, straight through the publisher's sidecar, to see
the drop path:

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST http://localhost:13500/v1.0/publish/intake-pubsub/intake-notices \
  -H 'Content-Type: application/cloudevents+json' \
  -d '{"specversion":"1.0","type":"gov.example.permits.issued.v1","source":"/permits/api",
       "id":"P-7","time":"2026-03-04T08:30:00Z","datacontenttype":"application/json",
       "data":{"permitId":"P-7"}}'
# 204
```

The subscriber's log is the demo. Captured from the run above:

```
{"level":"INFO","msg":"subscriber listening","addr":":8080","maxAttempts":3}
{"level":"INFO","msg":"processed","eventId":"N-1001","attempts":1}
{"level":"WARN","msg":"delivery failed, asking for redelivery","eventId":"N-9001","attempt":1,"budget":3,"error":"record series code is not in the retention catalog: \"RS-999\""}
{"level":"WARN","msg":"delivery failed, asking for redelivery","eventId":"N-9001","attempt":2,"budget":3,"error":"record series code is not in the retention catalog: \"RS-999\""}
{"level":"WARN","msg":"parked to dead-letter topic","eventId":"N-9001","topic":"intake-notices-parked","attempts":3,"reason":"record series code is not in the retention catalog: \"RS-999\""}
{"level":"WARN","msg":"message parked for human review","eventId":"N-9001","reason":"record series code is not in the retention catalog: \"RS-999\""}
{"level":"WARN","msg":"dropping unexpected event type","eventId":"P-7","type":"gov.example.permits.issued.v1"}
{"level":"WARN","msg":"message parked for human review","eventId":"P-7","reason":"parked by the sidecar: the consumer never acknowledged this message"}
```

Two deliveries retried at the two-second interval `components/resiliency.yaml` sets, the third
spent the budget and parked the message, and the dead-letter subscription turned it into a record.
The dropped event arrived at the same dead-letter route without the subscriber publishing it —
that is the sidecar forwarding a DROP because the subscription declares a `deadLetterTopic`.

Look at the parked queue:

```bash
curl -s http://localhost:18301/parked/N-9001
# {"eventId":"N-9001","type":"gov.example.records.intake_notice.v1","source":"/records/intake-api",
#  "reason":"record series code is not in the retention catalog: \"RS-999\"","attempts":3,
#  "parkedAt":"2026-08-14T23:39:07Z","data":{"noticeId":"N-9001","agencyCode":"DPR","seriesCode":"RS-999","pageCount":40}}

curl -s -w ' [%{http_code}]\n' http://localhost:18301/parked/N-1001
# {"error":"no parked message with that event id"} [404]
```

The attempt counter is a real key in the state store, and its etag is the write count:

```bash
curl -s -D- http://localhost:13501/v1.0/state/intake-statestore/attempt::N-9001
# HTTP/1.1 200 OK
# Etag: 3
# {"attempts":3}
```

Tear down with `docker compose down -v`.

## Notes

**Where the stack demonstration runs.** Continuous integration brings this example's stack up on an isolated runner and asserts the demonstrated behavior on every push, so the claims here stay true. Locally, bringing it up creates and destroys container bridge networks on your machine; do that only where it is safe to.

**What was verified how.** The Go suite (`go test ./... -count=1`, 44 tests) and the full runtime
demo above were both run. The runtime demo was executed with plain `docker run` on a user-defined
network rather than `docker compose up`, because the machine this was built on has no compose
plugin; `smoke.sh` is that exact sequence, and `docker-compose.yml` mirrors it argument for
argument (same images, same flags, same environment, same ports). The compose file itself has not
been executed by the compose plugin — run `./smoke.sh` to reproduce the demo, or bring the compose
stack up on a machine that has the plugin. Everything else — the CloudEvents envelope passing through
the sidecar untouched, both declarative subscriptions loading, the `ETAG` capability on the state
store, the 409 on a stale etag, the two retries, the park, the dead-letter route, and the DROP
forwarding — was observed against daprd 1.15.5 and Redis 7.

**Secrets.** `components/*.yaml` carry `{env:REDIS_PASSWORD}`, which the sidecar resolves from its
own environment at startup. Nothing secret is in version control. In a real deployment that line
becomes a `secretKeyRef` into a secret store: same field, different source, and in neither case is
the value in the repository. `redis:6379` is a compose service name.

**Two budgets, on purpose.** The subscriber's `MAX_DELIVERY_ATTEMPTS` is 3 and the sidecar's retry
policy allows 8. The application budget should normally fire first, because the application knows
*why* processing failed and can put that reason in the parked record. The sidecar's policy only
takes over when the subscriber cannot answer at all — it is unreachable, or it 500s — and then the
parked record carries no reason, only the fact.

**Counter cleanup.** `attempt::` and `processed::` keys are written and never removed here. A
production version sets `metadata.ttlInSeconds` on those writes so the bookkeeping expires; it is
left out to keep the state code down to the one get/set the example is about.

**Same word, different job.** See [`../event-sourced-aggregate`](../event-sourced-aggregate). There
an event is the domain's record of what happened — immutable, replayable, the system of record,
and losing one loses history. Here an event is transport between two services — a notification
that something happened elsewhere, delivered at least once, deduplicated by id, and discardable
once acted on. Conflating the two is a common and expensive mistake: teams publish their domain
events onto a topic and let subscribers rebuild state from them, then discover that a broker's
retention window is not an audit trail, that redelivery reorders history, and that a consumer's
view can never be rebuilt because the events it needed have aged out. Store the domain's events;
publish notifications about them.

All names, agency codes, record series codes, and case data here are synthetic.
