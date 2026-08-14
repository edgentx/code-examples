output "config_directory" {
  description = "Directory the dev configuration set was rendered to."
  value       = module.tenant_config.config_directory
}

output "tenant_config_paths" {
  description = "Rendered configuration file for each dev tenant, keyed by tenant key."
  value       = module.tenant_config.tenant_config_paths
}

output "manifest_path" {
  description = "Manifest indexing the rendered dev configuration files."
  value       = module.tenant_config.manifest_path
}

output "sizing_plan" {
  description = "Resolved capacity for each dev tenant."
  value       = module.tenant_config.sizing_plan
}

# The sensitive marking has to be repeated here. Dropping it is a plan-time
# error, not a silent leak -- which is the whole value of marking it in the
# module.
output "bootstrap_tokens" {
  description = "Bootstrap secret for each dev tenant. Generated at apply time; read it with `terraform output -json bootstrap_tokens`."
  value       = module.tenant_config.bootstrap_tokens
  sensitive   = true
}
