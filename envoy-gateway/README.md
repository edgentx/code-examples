# Envoy edge gateway (Envoy, Go)

**Requirement this addresses:** TLS termination, header sanitization, and timeout and retry policy at the edge, as reviewable configuration.

An Envoy proxy in front of two small Go services. The gateway terminates TLS, routes by path,
strips the identity headers a client might forge, stamps its own correlation identifier, bounds
every route with a timeout and a retry policy, and writes a JSON access log line for each request.

The two upstreams contain none of that. They have no TLS code, no header allow-list, no timeout
policy, and no request logging: they echo the request they received as JSON, which is what makes
the gateway's behavior observable. Whatever the edge forwarded appears in the reply; whatever the
edge removed is missing from it. Every control an assessor asks about is one file,
[`envoy-gateway.yaml`](envoy-gateway.yaml), read top to bottom.

## What it demonstrates

- **TLS ends at the edge, once.** One listener, one certificate, one place to set the minimum
  protocol version and one place to rotate. The upstreams speak plain HTTP on the private network
  segment behind it and carry no certificate handling at all. The cleartext port has no route to
  any upstream — it only redirects — so no configuration mistake can quietly start serving in the
  clear.
- **Header sanitization as a gate, not a convention.** `request_headers_to_remove` drops
  `x-user-id`, `x-user-roles`, `x-user-email`, `x-tenant-id`, `x-forwarded-client-cert`, and the
  internal `x-envoy-*` routing overrides before any route runs. Anything the edge does not strip,
  the application will eventually trust — not on the day it is written, but three years later, in
  a handler nobody remembers.
- **A correlation identifier the caller cannot choose.** `preserve_external_request_id: false`
  discards an inbound `x-request-id` and generates a fresh one, and
  `always_set_request_id_in_response: true` returns it. The identifier in a support ticket is the
  identifier in the log, and a caller cannot collide with somebody else's or reuse one to make an
  audit trail read the way they would like.
- **Path routing with rewrite.** `/api/a` and `/api/b` are the gateway's path space, rewritten
  away before forwarding, so the services never learn the URLs their callers use.
  `path_separated_prefix` matches on segment boundaries, so `/api/attachments` is not silently
  answered by service A.
- **Timeouts and retries stated per route.** `/api/b/slow` gets one second overall and 300ms per
  attempt, so the 504 in the demonstration below comes from the gateway giving up rather than from
  a caller waiting on the slowest thing behind it. A retry policy without a per-try timeout never
  retries — the first attempt is allowed the whole budget — which is why both are always set
  together.
- **JSON access logging with named fields.** These lines are evidence, so the fields have names:
  start time, method, original path, authority, response code, response flags, duration, upstream
  host and cluster, bytes, user agent, request identifier. `response_flags` is the field that
  separates "the service returned an error" from "the edge gave up".

## Layout

| File | Contents |
| --- | --- |
| `envoy-gateway.yaml` | The whole edge: listeners, TLS, sanitization, routes, timeouts, retries, access log. |
| `generate-dev-certs.sh` | Generates the throwaway certificate authority and gateway certificate into `certs/`. |
| `docker-compose.yml` | The demonstration stack: gateway plus both upstreams on one network. |
| `Dockerfile` | Builds either upstream; `SERVICE` selects which command. |
| `smoke.sh` | Brings the stack up, asserts the demonstrations below, tears it down. |
| `cmd/service-a/main.go` | Upstream A. Answers `/api/a`; no slow endpoint. |
| `cmd/service-b/main.go` | Upstream B. Answers `/api/b`; `/slow` sleeps past the route timeout. |
| `internal/echo/echo.go` | The shared handler: echoes the request it received as JSON. |
| `internal/echo/echo_test.go` | Table-driven handler tests, including cancellation of the slow endpoint. |
| `certs/.gitignore` | Excludes the generated TLS material. Nothing under `certs/` is ever committed. |

## Run it

The Go tests need nothing but a toolchain:

```bash
cd envoy-gateway
go test ./... -count=1
```

Validate the gateway configuration without starting anything. Generate the certificates first:
`--mode validate` opens every file the configuration references, so a missing certificate is a
validation failure.

```bash
./generate-dev-certs.sh
docker run --rm -v "$PWD:/workspace" -w /workspace \
  envoyproxy/envoy:v1.34-latest --mode validate -c envoy-gateway.yaml
# configuration 'envoy-gateway.yaml' OK
```

Bring the stack up:

```bash
./generate-dev-certs.sh
docker compose up --build
```

Ports on the host: gateway HTTPS `18443`, gateway HTTP `18100` (redirect only), upstream A `18101`
and upstream B `18102` — the upstreams are published only so you can compare a direct call with
the same call through the gateway.

Or run every demonstration below as an assertion and tear the stack down again:

```bash
./smoke.sh
```

`smoke.sh` uses plain `docker run` rather than the compose plugin, so it works on any machine with
a docker daemon; `docker-compose.yml` describes the same three containers, network, and ports.

### 1. A routed request reaches service A

`generate-dev-certs.sh` writes a local authority as well as the gateway certificate, so the
demonstrations verify TLS with `--cacert` rather than turning verification off. `-k` is a habit
worth not teaching.

```console
$ curl -sS --cacert certs/ca.crt https://localhost:18443/api/a/records/42
{
  "service": "service-a",
  "method": "GET",
  "path": "/records/42",
  "host": "localhost:18443",
  "headers": {
    "accept": "*/*",
    "user-agent": "curl/8.5.0",
    "x-edge-gateway": "envoy-edge",
    "x-envoy-expected-rq-timeout-ms": "1500",
    "x-envoy-external-address": "203.0.113.1",
    "x-envoy-original-path": "/api/a/records/42",
    "x-forwarded-for": "203.0.113.1",
    "x-forwarded-proto": "https",
    "x-request-id": "258b05d3-26f5-4bb0-8bac-8e5309771380"
  }
}
```

The public path was `/api/a/records/42`; service A was asked for `/records/42`. The connection is
verified against the generated authority:

```console
$ curl -sS -o /dev/null -v --cacert certs/ca.crt https://localhost:18443/api/a/healthz
* SSL connection using TLSv1.3 / TLS_AES_256_GCM_SHA384 / X25519 / RSASSA-PSS
*  subject: O=Edgent Code Examples; CN=localhost
*  issuer: O=Edgent Code Examples; CN=envoy-gateway development authority
*  SSL certificate verify ok.
```

### 2. Forged identity headers do not survive the edge

The same request, with a client asserting who it is and picking its own correlation identifier:

```console
$ curl -sS --cacert certs/ca.crt \
    -H 'x-user-id: 900001' \
    -H 'x-user-roles: records-administrator' \
    -H 'x-forwarded-client-cert: By=spoofed;Subject="CN=someone-else"' \
    -H 'x-request-id: forged-correlation-id' \
    https://localhost:18443/api/b/status
{
  "service": "service-b",
  "method": "GET",
  "path": "/status",
  "host": "localhost:18443",
  "headers": {
    "accept": "*/*",
    "user-agent": "curl/8.5.0",
    "x-edge-gateway": "envoy-edge",
    "x-envoy-expected-rq-timeout-ms": "1500",
    "x-envoy-external-address": "203.0.113.1",
    "x-envoy-original-path": "/api/b/status",
    "x-forwarded-for": "203.0.113.1",
    "x-forwarded-proto": "https",
    "x-request-id": "759756b0-1db0-45cd-8b15-a7430a65cd59"
  }
}
```

Four headers went in and none of them arrived. `x-user-id`, `x-user-roles`, and
`x-forwarded-client-cert` were removed; `x-request-id` was replaced with one the gateway
generated. What did arrive is what the edge itself asserts: `x-edge-gateway`, the real client
address in `x-forwarded-for` and `x-envoy-external-address`, the original path, and the scheme the
request actually used.

### 3. The slow route is cut off by the gateway

Service B sleeps five seconds on `/slow`. The route allows one second in total and 300ms per
attempt:

```console
$ curl -sS -i --cacert certs/ca.crt https://localhost:18443/api/b/slow
HTTP/1.1 504 Gateway Timeout
content-length: 24
content-type: text/plain
date: Fri, 14 Aug 2026 23:35:08 GMT
server: envoy
x-request-id: 7e26238f-919b-4190-82fe-e9a155557916

upstream request timeout

$ curl -sS -o /dev/null -w 'total: %{time_total}s\n' --cacert certs/ca.crt \
    https://localhost:18443/api/b/slow
total: 0.921626s
```

Three attempts of 300ms each, then the answer, in under a second — the caller's worst case is set
by this file, not by the slowest thing behind it. The cleartext port refuses to serve the same
request in the clear:

```console
$ curl -sS -i http://localhost:18100/api/a/records/42
HTTP/1.1 301 Moved Permanently
location: https://localhost:18443/api/a/records/42
date: Fri, 14 Aug 2026 23:35:09 GMT
server: envoy
content-length: 0
```

### 4. The access log

One line per request, on stdout, wrapped here for reading — the real line is a single object:

```json
{
  "authority": "localhost:18443",
  "bytes_sent": 24,
  "duration_ms": 917,
  "method": "GET",
  "path": "/api/b/slow",
  "protocol": "HTTP/1.1",
  "request_id": "39b936b7-6bd0-4422-a026-7b07d457ced3",
  "response_code": 504,
  "response_flags": "URX,UT",
  "start_time": "2026-08-14T23:35:08.654Z",
  "upstream_cluster": "service_b",
  "upstream_host": "203.0.113.3:8080",
  "user_agent": "curl/8.5.0"
}
```

`URX,UT` is the whole story of that request: retries exhausted, upstream timeout. `path` is what
the caller asked for, not the rewritten path that was forwarded, so the log answers questions
about the published interface. `request_id` is the same identifier the caller was handed in the
response.

## Notes

**Where the stack demonstration runs.** Continuous integration brings this example's stack up on an isolated runner and asserts the demonstrated behavior on every push, so the claims here stay true. Locally, bringing it up creates and destroys container bridge networks on your machine; do that only where it is safe to.

All data here is synthetic: two services that echo their input, and no records behind them. The
TLS material is generated on demand by `generate-dev-certs.sh`, expires in 90 days, and is never
committed — `certs/.gitignore` excludes the directory, and the repository's publication gate fails
the build on a committed key.

The demonstration network uses `203.0.113.0/24`, the reserved documentation range from RFC 5737,
so the addresses in the captured output above are documentation addresses rather than a real
network's numbering.

Header stripping at the edge is one half of a pair. See
[`../authorization-sidecar`](../authorization-sidecar) for the other: the policy beside each
service independently refuses to trust a raw identity header, and derives the caller from a
verified token instead. Either control alone is a single point of failure — an edge rule that is
edited by mistake, or a service that is reached by something other than the edge. Together they
are defense in depth, and each is reviewable on its own.

The JSON access log is the edge half of the observability story: it says what the gateway decided
and how long the whole request took. See [`../otel-observability`](../otel-observability) for the
inside of that duration — distributed traces across the services the gateway routed to. The
correlation identifier the gateway stamps is what joins the two.
