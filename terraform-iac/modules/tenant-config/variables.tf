# Every input is typed and described. An untyped `map(any)` accepts a typo in a
# nested attribute and fails somewhere far away at apply time; an object type
# rejects it at plan time, on the line that is wrong.

variable "environment" {
  description = "Environment this configuration set belongs to. Becomes a directory name and is stamped into every rendered file so a file found on disk can be traced back to the state that produced it."
  type        = string

  validation {
    condition     = contains(["dev", "test", "stage", "prod"], var.environment)
    error_message = "environment must be one of: dev, test, stage, prod. Add a new environment by extending this list and creating environments/<name>/, not by passing a free-form string."
  }
}

variable "output_directory" {
  description = "Directory the rendered tenant configuration files and the manifest are written to. Created if it does not exist."
  type        = string

  validation {
    condition     = length(trimspace(var.output_directory)) > 0
    error_message = "output_directory must not be empty."
  }
}

variable "tenants" {
  description = "Tenants to render configuration for, keyed by tenant key. The key is used verbatim as the config file name and as the tenant identifier inside the rendered file, so it is an identity, not a label."

  type = map(object({
    display_name   = string
    sizing         = string
    retention_days = number
    feature_flags = object({
      public_portal = bool
      audit_export  = bool
      sso_required  = bool
    })
  }))

  # Rule 1: the map key is an identifier, and identifiers that flow into file
  # names, DNS labels and object names have to be safe in all three. This is the
  # validation whose message is written for the operator who tripped it: it names
  # the offending keys and shows the fix, because "invalid tenants value" would
  # send them reading the module source.
  validation {
    condition = alltrue([
      for key in keys(var.tenants) :
      can(regex("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", key)) && length(key) <= 63
    ])
    error_message = format(
      "Tenant keys must be DNS-1123 labels: lowercase letters, digits and hyphens only, beginning and ending with a letter or digit, 63 characters or fewer. Rejected: %s. Rename the map key itself (for example \"County Records\" becomes \"county-records\") and move the human-readable form to display_name; the key is used verbatim as a file name and as a tenant identifier, so it is never normalized for you.",
      join(", ", [
        for key in keys(var.tenants) :
        format("%q", key)
        if !(can(regex("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$", key)) && length(key) <= 63)
      ])
    )
  }

  # Rule 2: sizing is a closed set. Free-form sizing means every consumer invents
  # its own mapping from string to capacity.
  validation {
    condition = alltrue([
      for tenant in values(var.tenants) : contains(["small", "medium", "large"], tenant.sizing)
    ])
    error_message = "Each tenant's sizing must be one of: small, medium, large. These are the only profiles defined in locals.sizing_profiles; add a profile there before using a new name."
  }

  # Rule 3: a feature flag combination that is a policy, not a preference. A
  # tenant that exposes a public portal is handling submissions from outside the
  # agency, so the export that evidences those submissions is not optional.
  validation {
    condition = alltrue([
      for tenant in values(var.tenants) : tenant.feature_flags.audit_export
      if tenant.feature_flags.public_portal
    ])
    error_message = "A tenant with feature_flags.public_portal = true must also set feature_flags.audit_export = true. Public intake is externally submitted evidence; it has to be exportable for the record. Turn on audit_export, or turn off public_portal."
  }

  # Rule 4: retention has a floor and a ceiling, and turning on audit export
  # raises the floor to a year.
  validation {
    condition = alltrue([
      for tenant in values(var.tenants) :
      tenant.retention_days >= 30 && tenant.retention_days <= 3650
    ])
    error_message = "Each tenant's retention_days must be between 30 and 3650 inclusive."
  }

  validation {
    condition = alltrue([
      for tenant in values(var.tenants) : tenant.retention_days >= 365
      if tenant.feature_flags.audit_export
    ])
    error_message = "A tenant with feature_flags.audit_export = true must set retention_days to at least 365. An export window shorter than the review cycle it feeds is worse than no export at all."
  }

  validation {
    condition = alltrue([
      for tenant in values(var.tenants) : length(trimspace(tenant.display_name)) > 0
    ])
    error_message = "Each tenant's display_name must be non-empty. It is what appears in the interface; the map key is not a substitute."
  }
}
