# Distributed tracing with OpenTelemetry (Go)

**Requirement this addresses:** end-to-end request tracing and monitoring across services for incident response and performance management.

Two small services and a collector. The front desk takes the citizen's request and calls the
records office over HTTP to fetch the case file. One inbound request produces one trace with spans
from both services, a handful of low-cardinality metrics, and log lines carrying the trace id — so
"it was slow around 4:15" turns into a single trace showing exactly which hop spent the time.

## What it demonstrates

- **Context propagation across the network hop.** `otel.SetTextMapPropagator` installs W3C
  `propagation.TraceContext{}`; `otelhttp.NewTransport` injects the `traceparent` header on the way
  out and `otelhttp.NewHandler` extracts it on the way in. That, and nothing more, is what makes
  the records office's span a child of the front desk's rather than the root of a second,
  unconnected trace.
- **Automatic and hand-written instrumentation together.** otelhttp produces the server and client
  spans for free. The spans that carry meaning — `records.lookup` at the front desk, `archive.read`
  in the records office — are written by hand, with the case identifier on them.
- **Cardinality discipline, stated as a rule.** Metric attributes are the route *template* and the
  status *class*: at most a handful of time series per service, forever. The case identifier goes
  on the span, where searching for one value is the point. Getting this backwards is the failure
  everyone hits, and it takes down the metrics pipeline rather than announcing itself.
- **Logs joined to traces.** A `slog.Handler` decorator pulls `trace_id` and `span_id` off the
  active span context and stamps them on every record. The support desk pivots from a log line to
  the whole call chain with one identifier.
- **Tests that need no collector.** `tracetest.NewInMemoryExporter` and the metric SDK's
  `ManualReader` assert the span tree, the attributes, the adoption of an inbound `traceparent`,
  the counter and histogram values, and the trace id on a log record — all in process, no Docker,
  no network.
- **A collector that keeps the backend swappable.** Services speak OTLP to one place. Changing
  where traces are stored is an edit to `otel-collector.yaml`, not a redeploy of every service.

## Layout

| File | Contents |
| --- | --- |
| `cmd/frontdesk/main.go` | Citizen-facing binary. Serves `GET /requests/{caseID}`. |
| `cmd/records/main.go` | Downstream binary. Serves `GET /records/{caseID}`. |
| `internal/telemetry/telemetry.go` | SDK setup: resource, OTLP exporters, propagator, shutdown flush. |
| `internal/telemetry/instruments.go` | The request counter and latency histogram, and the cardinality rules. |
| `internal/telemetry/slog.go` | The slog handler that stamps `trace_id`/`span_id` on every record. |
| `internal/service/service.go` | Route templates, span naming, and the metric-recording middleware. |
| `internal/service/frontdesk.go` | Front desk handler and the traced outbound client. |
| `internal/service/records.go` | Records office handler and the synthetic case-file catalog. |
| `internal/service/run.go` | Shared process lifecycle: serve, drain, flush telemetry. |
| `internal/service/trace_test.go` | Span tree, attributes, inbound `traceparent` adoption, error status. |
| `internal/service/metrics_test.go` | Counter and histogram values, and the series-count guard. |
| `internal/telemetry/slog_test.go` | Trace correlation on log records, including through `With()`. |
| `otel-collector.yaml` | Collector pipelines: OTLP in, batch, debug out; production exporters commented. |
| `docker-compose.yml` | The demo stack: both services plus the collector. |
| `Dockerfile` | One build, two binaries, selected by the `SERVICE` build argument. |
| `smoke.sh` | Stack up, request, assert one trace across both services, tear down. |

## Run it

Tests first — they need nothing but Go 1.24:

```bash
cd otel-observability
go test ./... -count=1
```

Then the stack:

```bash
docker compose up --build -d
```

Ask for the deliberately slow case file. Its records are on an off-site shelf, which is the
synthetic stand-in for the storage tier that makes real requests slow:

```bash
curl -s -w '\nHTTP %{http_code} in %{time_total}s\n' \
  http://localhost:18200/requests/CASE-2026-0203
```

```
{"case_id":"CASE-2026-0203","custodian":"Off-site Archive","status":"released","pages":418}
HTTP 200 in 0.255967s
```

Both services logged that request, and both log lines carry the same trace id:

```
{"time":"2026-08-14T23:35:53.615411906Z","level":"INFO","msg":"case file requested",
 "service":"frontdesk","case_id":"CASE-2026-0203",
 "trace_id":"b8c3ead668edf90315d222ea6cd8af73","span_id":"d35861f097ced62b"}

{"time":"2026-08-14T23:35:53.86788005Z","level":"INFO","msg":"case file read",
 "service":"records","case_id":"CASE-2026-0203","pages":418,
 "trace_id":"b8c3ead668edf90315d222ea6cd8af73","span_id":"115b1584b79811f4"}
```

Take that trace id to the collector. `docker compose logs collector` prints what the debug exporter
received — the real capture below, with the boilerplate HTTP attributes trimmed:

```
2026-08-14T23:35:59.176Z  info  ResourceSpans #0
Resource attributes:
     -> service.name: Str(records)
     -> service.version: Str(0.1.0)
InstrumentationScope github.com/edgentx/code-examples/otel-observability
Span #0
    Trace ID       : b8c3ead668edf90315d222ea6cd8af73
    Parent ID      : 46853216488cce91
    ID             : 115b1584b79811f4
    Name           : archive.read
    Kind           : Internal
    Start time     : 2026-08-14 23:35:53.617534385 +0000 UTC
    End time       : 2026-08-14 23:35:53.868142912 +0000 UTC
Attributes:
     -> agency.case_id: Str(CASE-2026-0203)
     -> agency.shelf: Str(off-site)
     -> agency.case_found: Bool(true)
     -> agency.custodian: Str(Off-site Archive)
InstrumentationScope go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp 0.66.0
Span #0
    Trace ID       : b8c3ead668edf90315d222ea6cd8af73
    Parent ID      : 92f8ccaac0e0fe7f
    ID             : 46853216488cce91
    Name           : GET /records/{caseID}
    Kind           : Server
    Start time     : 2026-08-14 23:35:53.617499256 +0000 UTC
    End time       : 2026-08-14 23:35:53.868480498 +0000 UTC
Attributes:
     -> http.route: Str(/records/{caseID})
     -> http.response.status_code: Int(200)

ResourceSpans #1
Resource attributes:
     -> service.name: Str(frontdesk)
     -> service.version: Str(0.1.0)
InstrumentationScope go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp 0.66.0
Span #0
    Trace ID       : b8c3ead668edf90315d222ea6cd8af73
    Parent ID      : d35861f097ced62b
    ID             : 92f8ccaac0e0fe7f
    Name           : GET /records/{caseID}
    Kind           : Client
    Start time     : 2026-08-14 23:35:53.615578964 +0000 UTC
    End time       : 2026-08-14 23:35:53.869293146 +0000 UTC
Attributes:
     -> url.full: Str(http://records:8201/records/CASE-2026-0203)
     -> http.response.status_code: Int(200)
Span #1
    Trace ID       : b8c3ead668edf90315d222ea6cd8af73
    Parent ID      :
    ID             : f5bc50a62f844f1d
    Name           : GET /requests/{caseID}
    Kind           : Server
    Start time     : 2026-08-14 23:35:53.615355328 +0000 UTC
    End time       : 2026-08-14 23:35:53.869469236 +0000 UTC
Attributes:
     -> http.route: Str(/requests/{caseID})
     -> http.response.status_code: Int(200)
InstrumentationScope github.com/edgentx/code-examples/otel-observability
Span #0
    Trace ID       : b8c3ead668edf90315d222ea6cd8af73
    Parent ID      : f5bc50a62f844f1d
    ID             : d35861f097ced62b
    Name           : records.lookup
    Kind           : Internal
    Start time     : 2026-08-14 23:35:53.615401318 +0000 UTC
    End time       : 2026-08-14 23:35:53.869328742 +0000 UTC
Attributes:
     -> agency.case_id: Str(CASE-2026-0203)
     -> agency.upstream: Str(records)
     -> agency.records_status: Int(200)
```

Five spans, two services, one trace id, and the parent chain reads
`GET /requests/{caseID}` → `records.lookup` → `GET /records/{caseID}` (client) →
`GET /records/{caseID}` (server) → `archive.read`. The front desk span covers 253ms; `archive.read`
covers 251ms of it. That is the answer to "why was it slow": not the front desk, not the network —
the archive shelf.

A case file that does not exist is the failed-request half of the same story:

```bash
curl -s -o /dev/null -w 'HTTP %{http_code}\n' \
  http://localhost:18200/requests/CASE-2026-9999
```

```
HTTP 404
```

That trace carries `Status code : Error` and `Status message : records office returned 404 Not
Found` on the front desk's `records.lookup` span, so the failure is findable in the trace backend
without reading attributes.

The metrics arrive on the same pipeline. Note the attribute set and the exemplar:

```
Metric #1
Descriptor:
     -> Name: agency.request.duration
     -> Unit: s
     -> DataType: Histogram
HistogramDataPoints #0
Data point attributes:
     -> http.route: Str(/requests/{caseID})
     -> http.status_class: Str(2xx)
Count: 1
Sum: 0.253958
Min: 0.253958
Max: 0.253958

Metric #0
     -> Name: agency.request.count
NumberDataPoints #0
Data point attributes:
     -> http.route: Str(/requests/{caseID})
     -> http.status_class: Str(2xx)
Value: 1
Exemplars:
Exemplar #0
     -> Trace ID: b8c3ead668edf90315d222ea6cd8af73
     -> Span ID: f5bc50a62f844f1d
```

Two attributes, so the series count stays flat no matter how many case files are requested — and
the exemplar still carries a trace id, which is the supported way to get from a spiking latency
chart back to one slow request.

Tear down and run the whole demonstration as an assertion:

```bash
docker compose down
./smoke.sh
```

```
==> trace id 89df29b7008c574fafabacc6f42be6ce
PASS: one request, one trace (89df29b7008c574fafabacc6f42be6ce), 5 spans across both services
```

## Notes

**Where the stack demonstration runs.** Continuous integration brings this example's stack up on an isolated runner and asserts the demonstrated behavior on every push, so the claims here stay true. Locally, bringing it up creates and destroys container bridge networks on your machine; do that only where it is safe to.

Every case file, custodian, and identifier here is invented. There is no real records system behind
this, and the 250ms "off-site shelf" delay is a `time.Sleep`.

Host ports are offset — front desk `18200`, records `18201`, collector OTLP gRPC `14317` and HTTP
`14318` — so this stack runs beside the other examples in this repository without colliding.

The tests deliberately do not need the collector. `tracetest.NewInMemoryExporter` and a
`ManualReader` give exact assertions with no timing tolerance, which is what makes them useful as a
CI gate rather than as a demonstration. `smoke.sh` is the separate, Docker-dependent check that the
wiring in `docker-compose.yml` and `otel-collector.yaml` is real.

The collector config ships the `debug` exporter because that is what this demo runs. The Prometheus
and OTLP-to-a-trace-backend exporters are present but commented out: production wiring shown, not
production wiring pretended.

This is the service half of the observability story. See
[`../envoy-gateway`](../envoy-gateway) for the edge half — structured JSON access logs at the
gateway, where every request is recorded before it reaches any service. The gateway log tells you a
request arrived and what it returned; the trace here tells you what happened inside. Together they
cover a citizen's request end to end, and both are keyed by the same `traceparent` when the gateway
propagates it.
