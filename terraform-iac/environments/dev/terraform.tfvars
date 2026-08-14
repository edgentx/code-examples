# Synthetic values. Every tenant below is invented for this example.
#
# This file is the whole diff for a routine change: onboarding a tenant, moving
# one up a sizing tier, or extending a retention window is an edit here, and the
# plan it produces is what a reviewer approves.

output_root = "./generated"

tenants = {
  # Public intake is on, so audit_export must be on and retention must be at
  # least a year. The module rejects the combination that violates either.
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

  # Staff-only, so the public-portal rule does not apply and a shorter retention
  # window is permitted.
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

  # Audit export without a public portal: allowed, but it still pulls retention
  # up to the 365-day floor.
  "north-valley-records" = {
    display_name   = "North Valley Records Division"
    sizing         = "small"
    retention_days = 730
    feature_flags = {
      public_portal = false
      audit_export  = true
      sso_required  = false
    }
  }
}
