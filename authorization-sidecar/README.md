# Authorization sidecar (Envoy ext_authz, Open Policy Agent, Go)

**Requirement this addresses:** zero-trust architecture; authorization enforced at a policy enforcement point outside the application, fail-closed.

Three containers: a small Go document service, an Envoy proxy in front of it, and Open Policy
Agent beside the proxy. Envoy calls the policy engine over `ext_authz` on every request and
forwards nothing that the policy has not allowed. The Go service contains no authorization code at
all — no token parsing, no role check, no `if user.IsAdmin`. It reads the identity headers the
sidecar stamps and nothing else.

The result an agency can inspect: the rules governing who may read and who may delete are a
sixty-line policy file with its own test suite, reviewable and changeable without recompiling or
redeploying the application it governs.

## What it demonstrates

- **The application holds no authorization code.** `service.go` opens with the reason in block
  capitals. `service_test.go` includes the uncomfortable proof — handed a request carrying no
  identity whatsoever, the service answers it. That is not a bug to fix in the service; it is why
  the deployment has to guarantee there is no path to that port except through the sidecar.
- **And that guarantee is wiring, not a convention.** The proxy and the application authenticate
  each other with mutual TLS against a single authority, and each checks the name on the other's
  certificate. Sharing a network is not a control: anything else on the network could dial the
  application and skip every decision above it. With mutual TLS the set of callers that can
  complete a handshake with the application has one member, and `smoke.sh` proves it by dialing
  the application directly and being refused before a request exists. Verifying the *name* and not
  only the signature is the part that is easy to leave out: "signed by our authority" in a cluster
  means every workload in the mesh.
- **Closed-set policy construction.** `policy/authz.rego` starts at `default allow := false`,
  enumerates the two requests that may proceed unauthenticated (health and readiness, matched on
  method and exact path), then enumerates the authenticated rules with the role each requires.
  Everything else is already denied, so there is no deny list to keep in sync. A route added
  upstream tomorrow arrives denied rather than exposed.
- **Fail closed.** `failure_mode_allow: false` in `envoy.yaml` means that if the policy engine is
  unreachable, the request breaks. `smoke.sh` stops the policy container and asserts the 403 —
  including on the public health route, because the proxy is not deciding that health is public,
  the policy is. Failing open would convert an availability incident in one process into a
  confidentiality incident across every endpoint behind it, silently, with a 200 in the log.
- **A header is not a credential.** Identity is derived from the bearer token and nothing else.
  Four policy tests and four smoke assertions cover the forged-header case: a caller who sets
  `X-Authz-Subject: owner-omar` on its own request gets exactly the same denial as a caller who
  sends nothing, and a reader who forges the owner role still cannot delete. On an allowed
  request Envoy *sets* the identity headers rather than appending, so the forged value is
  overwritten before the application sees it.
- **Separation of read from destroy.** A reader may list and fetch; only an owner may delete. An
  authenticated auditor who holds neither role is refused. Authentication is not authorization.
- **Evidence.** Every decision, allow and deny alike, goes to the policy engine's decision log.
  The response to a caller who is probing says only `{"error":"forbidden"}` — which rule refused
  them is in the log, not in the reply.

## Layout

| File | Contents |
| --- | --- |
| `service.go` | The upstream HTTP service, and the block comment explaining why it holds no authorization code. |
| `documents.go` | The synthetic document store behind those endpoints. |
| `main.go` | Binds the listener, over mutual TLS when the deployment supplies the material. |
| `mtls.go` | The client-certificate rule: signed by our authority, *and* issued to the sidecar. |
| `mtls_test.go` | The rule as a table, plus a real handshake: the sidecar gets in, no certificate does not, and a certificate the same authority issued to another workload does not either. |
| `generate-dev-certs.sh` | Makes the throwaway authority and the two leaf certificates. Nothing under `certs/` is ever committed. |
| `service_test.go` | Table-driven handler tests, plus the two tests that prove the service derives nothing from identity. |
| `envoy.yaml` | Listener, the `ext_authz` filter, `failure_mode_allow: false`, and the two clusters. |
| `policy/authz.rego` | The closed-set policy: default deny, the open set, the authenticated rules, and the decision Envoy receives. |
| `policy/authz_test.rego` | 21 `opa test` cases. Most of them are denials. |
| `opa-config.yaml` | Policy engine configuration: the `ext_authz` gRPC plugin and decision logging. |
| `Dockerfile` | Static build of the service on an empty base — no shell in the image. |
| `docker-compose.yml` | The three-container demo stack. |
| `smoke.sh` | Brings the stack up, asserts all of the above against a running proxy, tears it down. |

## Run it

Tests need no Docker:

```bash
cd authorization-sidecar
go test ./... -count=1

curl -sSfL -o /tmp/opa https://openpolicyagent.org/downloads/v1.4.2/opa_linux_amd64_static
chmod +x /tmp/opa
/tmp/opa test --verbose policy/
```

Validate the proxy configuration without running it. Validation opens every file the
configuration references, including the TLS material, so generate that first:

```bash
./generate-dev-certs.sh
docker run --rm -v "$PWD:/workspace" -w /workspace \
  envoyproxy/envoy:v1.34-latest --mode validate -c envoy.yaml
```

Run the whole demonstration:

```bash
./smoke.sh
```

Or drive it by hand. The stack listens on `18000` (proxy), `18181` (policy engine REST API), and
`18080` (the application, published only so you can watch an attempt to skip the proxy fail).

```bash
./generate-dev-certs.sh
docker compose up -d --build
```

**Three calls, which are the whole claim.** Through the sidecar with a verified identity, the
request is served. Through the sidecar without one, it is refused by the policy. Straight at the
application, it is refused before it is a request at all:

```console
$ curl -s -o /dev/null -w '%{http_code}\n' \
    -H 'Authorization: Bearer t-rosalind-reader' http://localhost:18000/api/documents
200

$ curl -s -o /dev/null -w '%{http_code}\n' http://localhost:18000/api/documents
403

$ curl -s --resolve upstream:18080:127.0.0.1 --cacert certs/ca.crt \
    https://upstream:18080/api/documents
curl: (56) OpenSSL SSL_read: error:0A00045C:SSL routines::tlsv13 alert certificate required
```

There is no status code in the third case because there is no response. The application asked for
a client certificate, the caller had none, and the connection ended in the handshake. Present the
certificate issued to the proxy and the same call succeeds — which is the control sample proving
the refusal is the certificate check biting, and is exactly what that private key buys. In a
deployment it exists only inside the proxy's own container.

**No identity — refused before the application is ever reached:**

```console
$ curl -is http://localhost:18000/api/documents
HTTP/1.1 403 Forbidden
x-authz-decision: deny
content-length: 21
content-type: text/plain
server: envoy

{"error":"forbidden"}
```

**A verified reader — allowed, and the application is told who it is serving:**

```console
$ curl -is -H 'Authorization: Bearer t-rosalind-reader' http://localhost:18000/api/documents
HTTP/1.1 200 OK
content-type: application/json
content-length: 383
x-envoy-upstream-service-time: 0
server: envoy

{"identity":{"subject":"reader-rosalind","roles":"reader","decision":"allow","client_supplied_user_id":""},"documents":[{"id":"doc-1001","title":"Permit application, Riverbend crossing","case":"PRM-2024-0117"},{"id":"doc-1002","title":"Inspection report, Riverbend crossing","case":"PRM-2024-0117"},{"id":"doc-1003","title":"Records request acknowledgment","case":"REC-2024-0042"}]}
```

**A reader forging the owner identity — the forged headers are overwritten, not believed:**

```console
$ curl -is -H 'Authorization: Bearer t-rosalind-reader' \
       -H 'X-Authz-Subject: owner-omar' -H 'X-Authz-Roles: owner' -H 'X-User-Id: owner-omar' \
       http://localhost:18000/api/documents/doc-1002
HTTP/1.1 200 OK
content-type: application/json
content-length: 218
server: envoy

{"identity":{"subject":"reader-rosalind","roles":"reader","decision":"allow","client_supplied_user_id":"owner-omar"},"document":{"id":"doc-1002","title":"Inspection report, Riverbend crossing","case":"PRM-2024-0117"}}
```

The subject that reached the application is `reader-rosalind`, not the `owner-omar` the client
typed. `client_supplied_user_id` is echoed only to make the attempt visible; nothing branches on
it.

**That same reader attempting a delete — refused on the role, not on the identity:**

```console
$ curl -is -X DELETE -H 'Authorization: Bearer t-rosalind-reader' \
       http://localhost:18000/api/documents/doc-1002
HTTP/1.1 403 Forbidden
x-authz-decision: deny
content-length: 21

{"error":"forbidden"}
```

**The owner deleting the same document:**

```console
$ curl -is -X DELETE -H 'Authorization: Bearer t-omar-owner' \
       http://localhost:18000/api/documents/doc-1002
HTTP/1.1 200 OK
content-type: application/json
content-length: 149

{"identity":{"subject":"owner-omar","roles":"owner,reader","decision":"allow","client_supplied_user_id":""},"status":"deleted","message":"doc-1002"}
```

**Fail closed. Stop the policy engine and the door shuts, for everyone, on every route:**

```console
$ docker compose stop opa
$ curl -is -H 'Authorization: Bearer t-omar-owner' http://localhost:18000/api/documents
HTTP/1.1 403 Forbidden
server: envoy
content-length: 0

$ curl -is http://localhost:18000/health
HTTP/1.1 403 Forbidden
server: envoy
content-length: 0
```

Bring it back with `docker compose start opa` and reads resume within a second or two. Tear
everything down with `docker compose down -v`.

## Notes

**Where the stack demonstration runs.** Continuous integration brings this example's stack up on an isolated runner and asserts the demonstrated behavior on every push, so the claims here stay true. Locally, bringing it up creates and destroys container bridge networks on your machine; do that only where it is safe to.

All data is synthetic: the documents, the case numbers, the subjects, and the bearer tokens are
invented for this example. `policy/authz.rego` resolves a token against a static demo directory so
the stack runs with no identity provider attached; in a real deployment that block becomes a
signature verification against the provider's published keys, with roles read from the verified
claims. What does not change is the shape of the rule — `subject` is undefined unless a credential
was verified, and every authenticated rule depends on `subject`.

The demonstration stands up the policy engine as a container beside the proxy. In a cluster the
same three processes are containers in one pod and the proxy-to-policy call is loopback. The word
"sidecar" is accurate in both shapes: the decision point is deployed with the workload, not called
across the network as a shared service that becomes a single point of failure for every
application at once.

The TLS material here is generated, short-lived, and thrown away. In a cluster the same
certificates come from the mesh's own authority and are rotated automatically, and the name the
application checks is the proxy's workload identity rather than a name from a script. What does
not change is the rule: verify the signature *and* the name, because an authority that signs for
every workload has told you only that the caller exists.

Three siblings complete the picture:

- [`../records-service`](../records-service) is an application written against exactly this
  boundary. It declares the identity header it needs, implements no login, and holds no
  credential — because in a deployment the decision point is enforced beside it and it is
  unreachable except through it. This example is the wiring that makes that true.
- [`../envoy-gateway`](../envoy-gateway) is the other half of the forged-header story. This
  example proves the *policy* never reads a client-supplied identity header; the gateway strips
  those headers at the edge so they never enter the mesh at all. Belt and suspenders, deliberately
  — the control here holds even if the edge is misconfigured, and the edge holds even if a policy
  is later written carelessly.
- [`../rebac-authorization`](../rebac-authorization) is the decision this policy stands in for.
  Roles carried on a token answer "what kind of user is this"; relationship-based authorization
  answers "is this user related to this specific record", which is the question most agency
  records systems actually have to answer. The enforcement point shown here is unchanged by that
  substitution: Envoy still asks, and still forwards nothing it was not told to.
