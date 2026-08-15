# Infrastructure as code (Terraform)

**Requirement this addresses:** infrastructure as code, version-controlled, validated in the build, and reproducible across environments.

A map-driven Terraform module that renders one configuration file per tenant, plus a manifest
indexing the set, and an environment that instantiates it. The resources are deliberately
provider-independent — `hashicorp/local`, `hashicorp/random`, `hashicorp/null` — so the whole
example runs on a laptop or a build agent with **no cloud account, no credentials and no billable
resources**. What is on display is the discipline: typed and validated inputs, derived values
computed once, expansion keyed by identity, classified outputs, pinned versions, and a build that
proves all of it. Swap `local_file` for a cloud provider's resources and none of that changes.

## What it demonstrates

- **The environment is data, and the data is reviewed.** Onboarding a tenant, moving one up a
  sizing tier or extending a retention window is an edit to `environments/dev/terraform.tfvars`.
  The pull request is that diff plus the plan it produces; there is no console step, and no state
  the code does not describe.
- **Validation that bites, at plan time.** Six `validation` blocks on `tenants` alone reject a tenant key that is not
  a DNS-1123 label, a sizing tier outside the closed set, a public portal without an audit export,
  an audit export with a retention window under a year, an out-of-range retention window, and an
  empty display name. The DNS-1123 message names the offending keys and shows the rename, because
  an error that only says "invalid value" sends the operator to read the module source.
- **`for_each`, never `count`, over a map.** `count` addresses instances by list position, so
  removing the first of five tenants shifts every remaining index and plans four destroy-and-create
  pairs nobody asked for. `for_each` addresses them by key. Verified: removing one tenant from the
  three in `terraform.tfvars` plans `1 to add, 0 to change, 5 to destroy` — the departing tenant's
  four resources, plus the manifest that by definition indexes all of them. The other tenants are
  untouched.
- **Derived once, in `locals`.** The sizing vocabulary maps to capacity in exactly one table.
  Nothing downstream re-derives what "medium" means, so two tenants on the same tier cannot end up
  with different footprints.
- **Outputs are a classified interface.** Every output carries a `description`; `bootstrap_tokens`
  is marked `sensitive`, so it renders as `<sensitive>` in plan output and in bare
  `terraform output`, and a caller that re-exports it without repeating the marking fails at plan
  time. The secret itself is generated at apply and never lands in a rendered file — the file
  carries `bootstrap_credential_id`, which names the credential without being one.
- **Pinned toolchain and providers.** `versions.tf` constrains the CLI and every provider. See
  [Version pinning and the lock file](#version-pinning-and-the-lock-file).
- **A native test suite, not a smoke script.** `terraform test` applies the module and asserts on
  the files it actually wrote, then plans five invalid inputs and asserts each is rejected.

## Layout

| Path | Contents |
| --- | --- |
| `modules/tenant-config/main.tf` | Locals, the sizing table, and the `for_each` resources that render each tenant. |
| `modules/tenant-config/variables.tf` | Typed inputs and every rule, one `validation` block per rule. |
| `modules/tenant-config/outputs.tf` | Described outputs, including the `sensitive` one. |
| `modules/tenant-config/versions.tf` | CLI and provider constraints, with the rationale. |
| `modules/tenant-config/tests/tenant_config.tftest.hcl` | Native `terraform test`: one apply run with assertions, five expected-failure runs. |
| `modules/tenant-config/README.md` | Module contract: inputs, outputs, rules, rendered file shape. |
| `environments/dev/main.tf` | The instantiation: one module block and three arguments, because behavior belongs in the module. |
| `environments/dev/variables.tf` | The tenant shape restated for review, plus dev-only guard rails. |
| `environments/dev/terraform.tfvars` | The synthetic dev tenant set. This file is the routine diff. |
| `environments/dev/outputs.tf` | Re-exports, with the sensitive marking repeated. |
| `environments/dev/versions.tf` | Constraints, and a note where the backend block would go. |
| `test.sh` | The full local gate, in the order CI runs it. |

## Run it

`terraform` 1.6 or newer. `init` downloads the three providers from the registry; everything after
that is offline.

```bash
cd terraform-iac

# Format, module tests, environment plan/apply/assert/destroy -- the whole gate.
./test.sh
```

Or step through it by hand:

```bash
cd terraform-iac
terraform fmt -check -recursive

cd modules/tenant-config
terraform init -backend=false
terraform validate
terraform test

cd ../../environments/dev
terraform init
terraform validate
terraform plan
terraform apply -auto-approve

cat generated/dev/manifest.json
cat generated/dev/harbor-district-clerk.json
terraform output -json bootstrap_tokens   # sensitive: shown only when asked for by name

terraform destroy -auto-approve
```

`test.sh` uses `terraform` if it is on `PATH` and falls back to `tofu`; override with
`TERRAFORM_BIN=/path/to/binary`. OpenTofu is HCL-compatible and accepts every command used here,
including `tofu test`.

## Remote state and workspaces

**No backend is configured in this example and no backend credentials exist anywhere in this
repository.** State is local, which is what makes the example runnable with nothing but a
Terraform binary. A real estate does not run this way, and the conventions are worth stating.

A remote backend is declared per environment, in the environment's `terraform` block — never in
the module, which must stay backend-agnostic to be reusable:

```hcl
terraform {
  backend "s3" {
    bucket  = "<state-bucket>"
    key     = "tenant-config/dev/terraform.tfstate"
    region  = "<region>"
    encrypt = true
    # State locking. Without it, two concurrent applies race and the loser's
    # resources become orphans the next plan cannot see. Native S3 locking
    # (`use_lockfile`) needs Terraform 1.10 or newer; before that the same job
    # is done by a `dynamodb_table` lock table.
    use_lockfile = true
  }
}
```

The shape differs by backend — `azurerm` takes a container and leases the blob, `gcs` takes a
bucket, `http` takes lock and unlock endpoints — but three properties are non-negotiable
regardless of which one you use:

- **Encryption at rest, and access controlled like a credential store.** State holds every
  generated secret in plain text. In this example that is `bootstrap_tokens`; in a real estate it
  is database passwords and private keys. Anyone who can read state can read those. Treat read
  access to the state bucket as equivalent to read access to the secrets it describes.
- **Locking.** Two engineers applying at once against unlocked state is how resources become
  orphaned: the second write clobbers the first, and the resources the first created are no longer
  tracked by anything.
- **One state per environment.** The `key` above carries the environment in its path. Separate
  state means a mistake in dev cannot plan a destroy in production, and the blast radius of a
  corrupted or lost state file is one environment.

**Why workspaces are the wrong tool for environment separation.** Terraform workspaces give you
multiple states behind *one* configuration and *one* backend. That is the problem: dev and
production then share a state bucket and its access policy, so the isolation is a naming
convention rather than a boundary. They share a configuration too, so environment differences are
expressed as `terraform.workspace == "prod" ? … : …` conditionals scattered through the code — the
hardest kind of infrastructure change to review, because the file you are reading does not tell
you which branch production takes. And the selected workspace is ambient CLI state, not something
the checkout records, so `terraform apply` from the wrong workspace is one forgotten
`terraform workspace select` away and looks identical in the shell.

Directory-per-environment, as here, makes the environment explicit in the path, lets each
environment carry its own backend, its own credentials and its own approval rules, and keeps the
differences between environments in a tfvars file where a reviewer can see all of them at once.
Workspaces earn their place for short-lived parallel copies of the *same* environment — a
per-branch preview stack, a scratch copy for reproducing a bug — not for the dev/stage/prod ladder.

## Version pinning and the lock file

`required_version` and every provider constraint are pinned in `versions.tf`. This is not
housekeeping. An unpinned provider is a silent dependency on whatever was published this morning,
and the consequence is that the plan a change board approved is not the plan that gets applied —
the same configuration produces a different diff on a reviewer's laptop than it did in the
pipeline. Pinning makes "the configuration did not change" a claim the build can actually support.

The dependency lock file (`.terraform.lock.hcl`) is the second half of that. It records the exact
provider versions and package checksums a checkout resolved to, so `init` reinstalls the same
builds and verifies them against the recorded hashes.

**In your own repository, commit it.** It belongs in version control for the same reason
`package-lock.json` or `go.sum` does.

**This example gitignores it, deliberately.** The lock file records provider addresses and
checksums for the *registry the binary that generated it uses*: `registry.terraform.io` for
Terraform, `registry.opentofu.org` for OpenTofu. This example is verified with both binaries, so a
committed lock file would be correct for one and wrong for the other — CI would either fail
checksum verification or rewrite a tracked file on every run. That is a property of shipping a
dual-binary example, not advice for a real estate. `versions.tf` still constrains the versions,
so the pinning that matters for review is intact either way.

## Notes

All tenant names, display names, sizing and retention values here are synthetic and invented for
this example. There are no account identifiers, no regions, no endpoints and no credentials in this
directory; the only secret material is `bootstrap_tokens`, which `random_password` generates at
apply time into local state and which is never written to a rendered file or committed.

`.gitignore` covers `.terraform/`, `*.tfstate*`, `.terraform.lock.hcl`, and both generated output
directories (`generated/`, `.tftest-output/`). Nothing an `apply` or a `test` produces is tracked.

Provider-independence is the whole trick that makes this runnable offline, and it is also the honest
limit of the example: it demonstrates how infrastructure code is structured, validated and gated,
not a deployed footprint. The gating half of that story continues in
[`../accessibility-gated-ci`](../accessibility-gated-ci), where a build step is the enforcement
mechanism rather than a report.
