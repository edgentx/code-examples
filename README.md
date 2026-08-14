# Edgent Code Examples

[![CI](https://github.com/edgentx/code-examples/actions/workflows/ci.yml/badge.svg)](https://github.com/edgentx/code-examples/actions/workflows/ci.yml)

Runnable, self-contained examples of how Edgent builds software: domain-driven design with event
sourcing, hexagonal architecture, behavior-driven testing, accessibility as a merge gate,
zero-trust authorization enforced beside the application, event-driven services, and governed data
pipelines.

**This repository holds code only.** Every directory is an independent example with its own README,
its own tests, and a toolchain named in it. Each README opens with the one-line agency ask the
example answers. Nothing here is copied from a private repository: every example is a fresh,
minimal implementation written for this repository against synthetic or public data.

## Examples

| Example | Language / stack | Answers |
| --- | --- | --- |
| [`event-sourced-aggregate`](event-sourced-aggregate) | Go | Every change to a case record is captured, attributable, and reconstructible for audit. |
| [`hexagonal-service`](hexagonal-service) | Go, SQLite | The business rules are not welded to a vendor database; the store can change without rewriting the system. |
| [`gherkin-driven-testing`](gherkin-driven-testing) | Go, godog | The acceptance criteria in the contract are the tests that run, so "done" means the same thing to both sides. |
| [`accessibility-gated-ci`](accessibility-gated-ci) | HTML, Playwright, axe | Section 508 conformance is enforced by the build, not promised in a document. |
| [`rebac-authorization`](rebac-authorization) | OpenFGA, Go | Who can see a document is decided by an auditable model, not by conditionals scattered through the code. |
| [`medallion-pipeline`](medallion-pipeline) | Python, pandas | Bad data is caught and set aside with a reason, not quietly averaged into the briefed report. |
| [`authorization-sidecar`](authorization-sidecar) | Envoy, OPA, Go | Zero-trust authorization is enforced beside the application, not inside it. |
| [`envoy-gateway`](envoy-gateway) | Envoy, Go | The edge terminates TLS, sanitizes what clients send, and enforces timeouts and retries as reviewable configuration. |
| [`otel-observability`](otel-observability) | Go, OpenTelemetry | When a citizen reports a slow or failed request, that exact request can be found across every service that touched it. |
| [`dapr-pubsub`](dapr-pubsub) | Go, Dapr, Redis | A message is either processed or visibly parked for a human, never quietly dropped. |
| [`terraform-iac`](terraform-iac) | Terraform | The environment is defined in reviewable code and validated by the build, not assembled by hand in a console. |

## Running everything

Each example runs on its own; see its README. Continuous integration runs all of them on every
push, plus a publication gate:

```bash
scripts/check-no-committed-secrets.sh
```

That gate scans every tracked text file for credentials, key material, and the addresses of
non-public systems, and fails the build on the first match. It is the reason this repository can
be public.

## Standards

The examples are held to the same bar as production work: American English throughout, errors
handled and named rather than swallowed, table-driven tests, and no example larger than can be
read in one sitting.
