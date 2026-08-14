# Every output carries a description. Outputs are the module's public surface;
# an undescribed one is an undocumented function return.

output "config_directory" {
  description = "Directory the rendered files were written to."
  value       = local.config_directory
}

output "tenant_config_paths" {
  description = "Path of the rendered configuration file for each tenant, keyed by tenant key."
  value       = { for key, file in local_file.tenant_config : key => file.filename }
}

output "manifest_path" {
  description = "Path of the manifest indexing every rendered tenant configuration file."
  value       = local_file.manifest.filename
}

output "sizing_plan" {
  description = "Resolved capacity for each tenant after the sizing tier was expanded, keyed by tenant key. Exposed so a caller can assert on capacity without re-deriving it from the tier name."
  value       = { for key, tenant in local.tenants : key => tenant.profile }
}

output "bootstrap_credential_ids" {
  description = "Identifier of the bootstrap credential issued to each tenant. Safe to log and to reference from a ticket; it names the credential without being one."
  value       = { for key, id in random_uuid.bootstrap_credential : key => id.result }
}

# The convention worth copying: a value that must not appear in CI logs, in a
# plan posted to a pull request, or in `terraform output` run over someone's
# shoulder is marked sensitive at the point it leaves the module. Marking it
# here means every caller that re-exports it must mark it too, or the plan
# fails -- the classification propagates instead of relying on the next author
# remembering. Retrieve it deliberately:
#   terraform output -json bootstrap_tokens
output "bootstrap_tokens" {
  description = "Bootstrap secret issued to each tenant, keyed by tenant key. Generated at apply time and held only in state; never committed."
  value       = { for key, password in random_password.bootstrap_token : key => password.result }
  sensitive   = true
}
