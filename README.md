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
