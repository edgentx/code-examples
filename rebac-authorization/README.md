# Relationship-based authorization (OpenFGA, Go)

**Agency ask this answers:** "Show me that who can see this document is decided by an auditable model, not by if-statements scattered through the code."

A document-sharing scenario for a county records office, modeled as relationships rather than
roles. `model.fga` says what a relation means once; the store holds the facts (who is in which
department, which folder a record is filed in, who is walled off from what); every access decision
is a query against those two things. Application code never re-derives the rules — it asks
`check(user, can_view, document)` and gets an answer that can be traced back to named tuples.

## What it demonstrates

- **Union.** `can_view: (viewer or editor or owner) but not blocked`. Three ways to reach a read,
  written in one place. Adding a fourth is a one-line model change, not a search for every branch
  in the codebase that guessed at the rule.
- **Inheritance through the folder tree.** `viewer from parent` on a document, and again on a
  folder, so custody flows down a nested filing structure. No tuple is written against the nested
  folder or the documents inside it; the tests prove a two-hop inheritance really resolves.
- **Group membership as a userset rewrite.** `member from organization` says "whoever is a member
  of the organization related to this folder". Adding someone to the department roster grants them
  reads on everything filed under it, with nothing written against a folder or a document.
- **Deny by relationship.** A `blocked` tuple subtracts a person from `can_view` even though the
  inheritance path that would grant it is still intact. The test suite asserts both halves — the
  underlying `viewer` relation is still `true` for the blocked user, and `can_view` is `false` —
  because that is what proves an exclusion is doing the denying rather than a missing grant.
- **Retention as a modeled rule.** `can_delete` is `owner from parent` only. The author of a record
  can read, edit, and share it and still cannot destroy it; only the custodian of the folder it is
  filed in can. That is a policy sentence an agency can point at during an audit.
- **The list query, not just the point query.** `ListObjects` answers "what can this person see",
  walking the same relationships in reverse, so an access review cannot disagree with the
  individual checks.

## Layout

| File | Contents |
| --- | --- |
| `model.fga` | The authorization model in OpenFGA DSL, schema 1.1. The artifact a reviewer reads. |
| `model.json` | The same model in the form the write API accepts. Generated — see below. |
| `harness.go` | Store creation, model write, tuple seeding, and thin `Allowed` / `Visible` wrappers. No authorization logic. |
| `authorization_test.go` | Table-driven allow and deny cases, each naming the relationship path it proves, plus the `ListObjects` query. |
| `store.fga.yaml` | The same scenario and assertions as an `fga` CLI store test — no server, no Go. |
| `docker-compose.yml` | A single OpenFGA server for local runs. |
| `smoke.sh` | Brings the server up, runs the Go checks, the CLI store test, and a plain-HTTP check; tears down. |

## Run it

Start a server, then point the tests at it:

```bash
cd rebac-authorization
docker compose up -d
OPENFGA_API_URL=http://localhost:8080 go test ./... -count=1 -v
docker compose down
```

Without Compose, the equivalent is one container:

```bash
docker run -d --name openfga -p 8080:8080 openfga/openfga:v1.8.4 run
OPENFGA_API_URL=http://localhost:8080 go test ./... -count=1 -v
docker rm -f openfga
```

With `OPENFGA_API_URL` unset the tests skip, so a plain `go test ./...` across the repository needs
no server and no container runtime:

```bash
go test ./... -count=1
```

Everything at once, including a check issued over plain HTTP with no SDK:

```bash
./smoke.sh
```

The [`fga` CLI](https://github.com/openfga/cli) evaluates `store.fga.yaml` in process, which is the
fastest way for a policy reviewer to change a rule and see which assertions move:

```bash
fga model test --tests store.fga.yaml
```

`model.json` is generated from `model.fga` and the DSL is the source of truth. Regenerate it after
any model change:

```bash
fga model transform --file model.fga | jq . > model.json
```

## Notes

The organization, folders, documents, and people are synthetic; the scenario is a county public
records office invented for this example.

Two representations of the model are committed on purpose. `model.fga` is what a person reviews —
it is the shape of the DSL that makes union, inheritance, and exclusion legible at a glance.
`model.json` is what the write API accepts, and the Go tests embed it so that running them needs
nothing but a server. Keeping the DSL as the source and regenerating the JSON means a reviewer and
the machine are never reading different rules.

The scenario is expressed twice as well, once in `harness.go` and once in `store.fga.yaml`. That is
deliberate: the Go path proves the model against a live server the way an application would reach
it, and the CLI path proves the same behavior with neither a server nor a Go toolchain, which is
the form a records officer can be handed.

The server here stores everything in memory, so state disappears with the container and every test
run creates its own store. A deployment would set `OPENFGA_DATASTORE_ENGINE` to a managed database
and put authentication in front of the API; the model and the tuples do not change.
