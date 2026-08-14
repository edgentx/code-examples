# Module: `tenant-config`

Renders one reviewable configuration file per tenant from a single `tenants` map, plus a manifest
indexing the set. Every rule about what a tenant may be — key shape, sizing vocabulary, feature
flag combinations, retention floors — is enforced here, at plan time, so an invalid tenant never
reaches an apply.

The module composes only `hashicorp/local`, `hashicorp/random` and `hashicorp/null`. It creates
nothing in any cloud and needs no credentials.

## Usage

```hcl
module "tenant_config" {
  source = "../../modules/tenant-config"

  environment      = "dev"
  output_directory = "./generated"

  tenants = {
    "harbor-district-clerk" = {
      display_name   = "Harbor District Clerk"
      sizing         = "medium"
      retention_days = 1095
      feature_flags = {
        public_portal = true
        audit_export  = true
        sso_required  = true
      }
    }
  }
}
```

## Inputs

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| `environment` | `string` | yes | Environment this configuration set belongs to. Must be one of `dev`, `test`, `stage`, `prod`. Becomes a directory name and is stamped into every rendered file. |
| `output_directory` | `string` | yes | Directory the rendered files and the manifest are written to. The module appends `/<environment>`. Created if absent. |
| `tenants` | `map(object({...}))` | yes | Tenants to render, keyed by tenant key. See the object shape and the rules below. |

`tenants` value object:

| Attribute | Type | Description |
| --- | --- | --- |
| `display_name` | `string` | Human-readable name. Must be non-empty. |
| `sizing` | `string` | One of `small`, `medium`, `large`. Expanded to replicas, CPU and memory by `locals.sizing_profiles`. |
| `retention_days` | `number` | Retention window, 30 to 3650 inclusive. |
| `feature_flags.public_portal` | `bool` | Tenant accepts submissions from outside the agency. |
| `feature_flags.audit_export` | `bool` | Tenant's record set is exportable for review. |
| `feature_flags.sso_required` | `bool` | Tenant requires federated sign-in. |

### Rules the module enforces

| Rule | Rejected input |
| --- | --- |
| Tenant keys are DNS-1123 labels, 63 characters or fewer | `"Harbor District Clerk"`, `"riverbend_permits"` |
| `sizing` is a member of the closed set | `sizing = "extra-large"` |
| `public_portal = true` requires `audit_export = true` | public intake with no exportable record |
| `audit_export = true` requires `retention_days >= 365` | export window shorter than the review cycle |
| `retention_days` between 30 and 3650 | `retention_days = 7` |
| `display_name` is non-empty | `display_name = "  "` |

Each rule has its own `validation` block so the failure names the rule that fired. The DNS-1123
message lists the offending keys and shows the rename.

## Outputs

| Name | Type | Sensitive | Description |
| --- | --- | --- | --- |
| `config_directory` | `string` | no | Directory the files were rendered to. |
| `tenant_config_paths` | `map(string)` | no | Rendered file path per tenant key. |
| `manifest_path` | `string` | no | Path of the manifest indexing every rendered file. |
| `sizing_plan` | `map(object)` | no | Resolved replicas, CPU millicores and memory per tenant, after the tier was expanded. |
| `bootstrap_credential_ids` | `map(string)` | no | Identifier of the credential issued to each tenant. Safe to log or cite in a ticket. |
| `bootstrap_tokens` | `map(string)` | **yes** | The bootstrap secret per tenant. Generated at apply time, held only in state, never written into a rendered file. |

## Rendered file

```json
{
  "bootstrap_credential_id": "…",
  "environment": "dev",
  "features": { "audit_export": true, "public_portal": true, "sso_required": true },
  "retention": { "days": 1095 },
  "schema_version": 1,
  "sizing": { "cpu_millicores": 1000, "memory_mib": 2048, "replicas": 2, "tier": "medium" },
  "tenant": { "display_name": "Harbor District Clerk", "key": "harbor-district-clerk" }
}
```

`schema_version` travels with the document so a consumer can refuse a shape it does not
understand rather than guessing at it.

## Tests

```bash
terraform init -backend=false
terraform test
```

`tests/tenant_config.tftest.hcl` applies the module and asserts on the files it actually wrote,
then plans five deliberately invalid inputs and asserts each is rejected by the variable that
owns the rule.
