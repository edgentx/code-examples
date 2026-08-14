# tenant-config renders one reviewable configuration file per tenant, plus a
# manifest that indexes them. Nothing here talks to a cloud account: the point of
# the example is the shape of the code -- typed inputs, validated at plan time,
# expanded by identity, with derived values computed once -- not the resources.

locals {
  # The schema version travels with every rendered file. A consumer that reads
  # one off disk can refuse a shape it does not understand instead of guessing.
  schema_version = 1

  # Sizing is a closed vocabulary mapped to capacity in exactly one place. The
  # alternative -- each resource deciding what "medium" means -- is how two
  # tenants on the same tier end up with different footprints.
  sizing_profiles = {
    small  = { replicas = 1, cpu_millicores = 250, memory_mib = 512 }
    medium = { replicas = 2, cpu_millicores = 1000, memory_mib = 2048 }
    large  = { replicas = 4, cpu_millicores = 2000, memory_mib = 8192 }
  }

  config_directory = "${trimsuffix(var.output_directory, "/")}/${var.environment}"

  # One derived view of the inputs, built once and read by every resource below.
  # Repeating `local.sizing_profiles[each.value.sizing]` in three resources is
  # three places for the expression to drift.
  tenants = {
    for key, tenant in var.tenants : key => {
      key            = key
      display_name   = trimspace(tenant.display_name)
      sizing         = tenant.sizing
      profile        = local.sizing_profiles[tenant.sizing]
      feature_flags  = tenant.feature_flags
      retention_days = tenant.retention_days
      config_path    = "${local.config_directory}/${key}.json"
    }
  }

  # The rendered document. jsonencode over an object literal, rather than a
  # string template, so the output cannot be malformed by an unescaped value.
  rendered = {
    for key, tenant in local.tenants : key => jsonencode({
      schema_version = local.schema_version
      environment    = var.environment
      tenant = {
        key          = tenant.key
        display_name = tenant.display_name
      }
      sizing = {
        tier           = tenant.sizing
        replicas       = tenant.profile.replicas
        cpu_millicores = tenant.profile.cpu_millicores
        memory_mib     = tenant.profile.memory_mib
      }
      features  = tenant.feature_flags
      retention = { days = tenant.retention_days }

      # The file records which credential the tenant was issued, never the
      # credential. The secret itself leaves through a sensitive output and is
      # handed to the operator out of band; see outputs.tf.
      bootstrap_credential_id = random_uuid.bootstrap_credential[key].result
    })
  }
}

# A per-tenant credential identifier. Generated once and kept in state, so a
# re-apply does not rotate it out from under whatever is holding it.
resource "random_uuid" "bootstrap_credential" {
  for_each = local.tenants
}

# The bootstrap secret itself. Generated at apply time, so it is never written
# into this repository and never appears in a review diff. It is delivered
# through the `bootstrap_tokens` output, which is marked sensitive.
resource "random_password" "bootstrap_token" {
  for_each = local.tenants

  length      = 40
  special     = true
  min_upper   = 4
  min_lower   = 4
  min_numeric = 4
  min_special = 2

  # Rotate deliberately: changing the tenant's tier or retention window is not a
  # reason to invalidate a credential someone is already using.
  keepers = {
    tenant_key = each.key
  }
}

# for_each, not count.
#
# `count` addresses instances by list position: local_file.tenant[0],
# local_file.tenant[1]. Remove the first tenant from a five-tenant list and every
# remaining index shifts down by one, so the plan destroys and recreates four
# resources that nobody touched. `for_each` addresses them by key --
# local_file.tenant["harbor-district"] -- so removing a tenant plans exactly one
# destroy and leaves the others untouched. Any collection whose members have
# identity gets for_each; count is for "I want N of this identical thing".
resource "local_file" "tenant_config" {
  for_each = local.tenants

  filename             = each.value.config_path
  content              = "${local.rendered[each.key]}\n"
  file_permission      = "0640"
  directory_permission = "0750"
}

# The manifest is the index a downstream consumer reads first: what was rendered,
# where, and a digest of each file so drift is detectable without re-parsing.
# Keys are sorted so the file is byte-stable across runs and diffs stay readable.
resource "local_file" "manifest" {
  filename = "${local.config_directory}/manifest.json"
  content = "${jsonencode({
    schema_version = local.schema_version
    environment    = var.environment
    tenant_count   = length(local.tenants)
    tenants = [
      for key in sort(keys(local.tenants)) : {
        key           = key
        display_name  = local.tenants[key].display_name
        sizing        = local.tenants[key].sizing
        config_path   = local.tenants[key].config_path
        config_sha256 = sha256(local.rendered[key])
      }
    ]
  })}\n"
  file_permission      = "0640"
  directory_permission = "0750"
}

# A checkpoint per tenant, keyed by the digest of that tenant's rendered content.
# It carries no side effect; its job is to make the blast radius of a change
# visible in the plan. Edit one tenant's sizing and the plan replaces exactly one
# checkpoint, which is the property `count` would have destroyed.
#
# `terraform_data` is the modern built-in equivalent and needs no provider;
# null_resource is used here because the example pins the null provider
# explicitly and the two read identically.
resource "null_resource" "tenant_checkpoint" {
  for_each = local.tenants

  triggers = {
    tenant_key    = each.key
    config_sha256 = sha256(local.rendered[each.key])
  }
}
