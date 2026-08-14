# Version constraints are part of the module's contract, not housekeeping.
#
# An unpinned provider is a silent dependency on whatever was published this
# morning. The same configuration then produces a different plan on a reviewer's
# laptop than it did in the pipeline, and the diff a change board approved is not
# the diff that gets applied. Pin the CLI to a supported range and every provider
# to a compatible-version range, then let the dependency lock file (see the
# root README) fix the exact build for a given checkout.
#
# `>= 1.6.0` is the floor because the native test framework used by
# `tests/tenant_config.tftest.hcl` arrived in 1.6. The upper bound keeps a future
# major release from being adopted by accident.
terraform {
  required_version = ">= 1.6.0, < 2.0.0"

  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.5"
    }
    random = {
      source  = "hashicorp/random"
      version = "~> 3.6"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.2"
    }
  }
}
