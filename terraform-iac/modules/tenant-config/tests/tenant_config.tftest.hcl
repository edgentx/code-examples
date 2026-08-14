# Native test file for `terraform test` (Terraform 1.6+; `tofu test` on
# OpenTofu). Run it from the module directory:
#
#   terraform init -backend=false && terraform test
#
# Each `run` block is a plan or an apply against the module in this directory,
# with assertions evaluated afterwards. Apply runs are torn down when the file
# finishes, so the rendered files exist only for the duration of the test.

variables {
  environment      = "test"
  output_directory = "./.tftest-output"

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
    "riverbend-permits" = {
      display_name   = "Riverbend Permitting Office"
      sizing         = "small"
      retention_days = 90
      feature_flags = {
        public_portal = false
        audit_export  = false
        sso_required  = true
      }
    }
  }
}

# The load-bearing test: after an apply, the files are on disk with the content
# the inputs imply. Asserting on parsed JSON rather than on a substring means a
# reordered key does not fail the test and a wrong value cannot pass it.
run "renders_one_file_per_tenant" {
  command = apply

  assert {
    condition     = length(output.tenant_config_paths) == 2
    error_message = "Expected one rendered configuration file per tenant."
  }

  assert {
    condition = alltrue([
      for key, path in output.tenant_config_paths :
      fileexists(path) && endswith(path, "/${key}.json")
    ])
    error_message = "Every tenant must have a file on disk named for its key, verbatim."
  }

  assert {
    condition     = fileexists(output.manifest_path)
    error_message = "Expected a manifest alongside the rendered tenant files."
  }

  # The sizing tier expands to capacity in exactly one place, and the rendered
  # file carries the expansion rather than the tier name alone.
  assert {
    condition = (
      jsondecode(file(output.tenant_config_paths["harbor-district-clerk"])).sizing.replicas == 2 &&
      jsondecode(file(output.tenant_config_paths["riverbend-permits"])).sizing.replicas == 1
    )
    error_message = "Rendered replica count does not match the sizing profile for the tenant's tier."
  }

  assert {
    condition     = jsondecode(file(output.tenant_config_paths["harbor-district-clerk"])).environment == "test"
    error_message = "Rendered file must record the environment that produced it."
  }

  # The manifest indexes every tenant, in sorted order, with a digest.
  assert {
    condition = (
      jsondecode(file(output.manifest_path)).tenant_count == 2 &&
      [for entry in jsondecode(file(output.manifest_path)).tenants : entry.key] == [
        "harbor-district-clerk", "riverbend-permits"
      ]
    )
    error_message = "Manifest must list every tenant, sorted by key."
  }

  # The secret is issued, but what lands in the file is the identifier of the
  # credential, not the credential.
  assert {
    condition = alltrue([
      for key, path in output.tenant_config_paths :
      jsondecode(file(path)).bootstrap_credential_id == output.bootstrap_credential_ids[key]
    ])
    error_message = "Rendered file must reference the issued credential by identifier."
  }

  assert {
    condition     = length(nonsensitive(output.bootstrap_tokens)["harbor-district-clerk"]) == 40
    error_message = "Expected a 40-character bootstrap token per tenant."
  }
}

# The validations have to actually bite. Each of these plans a deliberately
# invalid input and asserts that the named variable rejects it; if a validation
# were deleted, the run would fail because the expected failure did not occur.

run "rejects_tenant_key_that_is_not_a_dns_1123_label" {
  command = plan

  variables {
    tenants = {
      "Harbor District Clerk" = {
        display_name   = "Harbor District Clerk"
        sizing         = "small"
        retention_days = 90
        feature_flags = {
          public_portal = false
          audit_export  = false
          sso_required  = true
        }
      }
    }
  }

  expect_failures = [var.tenants]
}

run "rejects_unknown_sizing_tier" {
  command = plan

  variables {
    tenants = {
      "riverbend-permits" = {
        display_name   = "Riverbend Permitting Office"
        sizing         = "extra-large"
        retention_days = 90
        feature_flags = {
          public_portal = false
          audit_export  = false
          sso_required  = true
        }
      }
    }
  }

  expect_failures = [var.tenants]
}

run "rejects_public_portal_without_audit_export" {
  command = plan

  variables {
    tenants = {
      "riverbend-permits" = {
        display_name   = "Riverbend Permitting Office"
        sizing         = "small"
        retention_days = 400
        feature_flags = {
          public_portal = true
          audit_export  = false
          sso_required  = true
        }
      }
    }
  }

  expect_failures = [var.tenants]
}

run "rejects_audit_export_with_short_retention" {
  command = plan

  variables {
    tenants = {
      "riverbend-permits" = {
        display_name   = "Riverbend Permitting Office"
        sizing         = "small"
        retention_days = 90
        feature_flags = {
          public_portal = false
          audit_export  = true
          sso_required  = true
        }
      }
    }
  }

  expect_failures = [var.tenants]
}

run "rejects_unknown_environment" {
  command = plan

  variables {
    environment = "sandbox"
  }

  expect_failures = [var.environment]
}
