# An environment is a thin instantiation: it names the module, supplies the
# values for this environment, and holds the state. All behavior lives in the
# module, so promoting a change from dev to stage is a change to a tfvars file
# and a review of the resulting plan -- not a second copy of the logic.

module "tenant_config" {
  source = "../../modules/tenant-config"

  environment      = "dev"
  output_directory = var.output_root
  tenants          = var.tenants
}
