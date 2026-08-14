# The environment re-declares the tenant shape rather than accepting `any` and
# forwarding it. The type is the review surface: a reviewer reading
# terraform.tfvars can see what a tenant is without opening the module.

variable "output_root" {
  description = "Directory beneath which rendered configuration is written. The module appends the environment name."
  type        = string
  default     = "./generated"
}

variable "tenants" {
  description = "Tenants to render for this environment. Keys must be DNS-1123 labels; the module enforces that and the rest of the rules."

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

  # Environment-level guard rails sit alongside the module's, not instead of
  # them. Dev is a place to try things; it is not a place to try things at the
  # capacity of the largest production tenant.
  validation {
    condition     = length(var.tenants) <= 10
    error_message = "This environment renders at most 10 tenants. Split a larger set across environments rather than growing one state file."
  }

  validation {
    condition = alltrue([
      for tenant in values(var.tenants) : tenant.sizing != "large"
    ])
    error_message = "Sizing \"large\" is not permitted in dev. Use \"small\" or \"medium\" here and prove the large profile in stage, where the capacity is budgeted."
  }
}
