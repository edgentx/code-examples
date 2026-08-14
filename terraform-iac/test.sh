#!/usr/bin/env bash
#
# test.sh -- the full local gate for this example, in the order CI runs it.
#
#   ./test.sh
#
# Uses `terraform` if it is on PATH, otherwise `tofu` (OpenTofu is
# HCL-compatible and accepts every command used here). Override with
# TERRAFORM_BIN=/path/to/binary.
#
# Everything is rendered into a temporary directory that is removed on exit, so
# running this leaves nothing behind in the checkout except .terraform/ and the
# local state files, both of which are gitignored.

set -euo pipefail

example_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
module_dir="${example_root}/modules/tenant-config"
environment_dir="${example_root}/environments/dev"

if [ -n "${TERRAFORM_BIN:-}" ]; then
  tf="${TERRAFORM_BIN}"
elif command -v terraform >/dev/null 2>&1; then
  tf="terraform"
elif command -v tofu >/dev/null 2>&1; then
  tf="tofu"
else
  echo "FAIL: neither terraform nor tofu is on PATH" >&2
  exit 1
fi
echo "using: $("${tf}" version | head -n 1)"

render_dir="$(mktemp -d)"
cleanup() {
  # Tear the applied configuration down even if an assertion failed, so a bad
  # run does not leave state pointing at files that no longer exist.
  if [ -f "${environment_dir}/terraform.tfstate" ]; then
    (cd "${environment_dir}" && "${tf}" destroy -auto-approve -input=false \
      -var "output_root=${render_dir}" >/dev/null 2>&1) || true
  fi
  rm -rf "${render_dir}"
}
trap cleanup EXIT

fail() {
  echo "FAIL: $1" >&2
  exit 1
}

# 1. Formatting. `fmt -check` is a gate, not a suggestion: an unformatted file
#    makes every later diff noisier than the change it contains.
echo '--- fmt -check -recursive'
(cd "${example_root}" && "${tf}" fmt -check -recursive)

# 2. The module on its own. -backend=false because a reusable module is never
#    initialized against a backend.
echo '--- module: init, validate, test'
(cd "${module_dir}" && "${tf}" init -backend=false -input=false >/dev/null)
(cd "${module_dir}" && "${tf}" validate)
(cd "${module_dir}" && "${tf}" test)

# 3. The environment. This proves the configuration evaluates end to end with no
#    account, no credentials and no network beyond the provider download in init.
echo '--- environments/dev: init, validate, plan'
(cd "${environment_dir}" && "${tf}" init -input=false >/dev/null)
(cd "${environment_dir}" && "${tf}" validate)
(cd "${environment_dir}" && "${tf}" plan -input=false -var "output_root=${render_dir}" >/dev/null)

echo '--- environments/dev: apply'
(cd "${environment_dir}" && "${tf}" apply -auto-approve -input=false \
  -var "output_root=${render_dir}" >/dev/null)

# 4. Assert on what was rendered, not on what the plan said it would render.
echo '--- assert rendered output'
for tenant in harbor-district-clerk riverbend-permits north-valley-records; do
  [ -f "${render_dir}/dev/${tenant}.json" ] || fail "missing rendered config for ${tenant}"
done
[ -f "${render_dir}/dev/manifest.json" ] || fail 'missing manifest'

rendered_count="$(find "${render_dir}/dev" -name '*.json' -not -name 'manifest.json' | wc -l)"
[ "${rendered_count}" -eq 3 ] || fail "expected 3 tenant files, found ${rendered_count}"

# The medium tier expands to 2 replicas; the small tier to 1. If the sizing
# profile table and the rendered file ever disagree, this is where it shows.
grep -q '"replicas":2' "${render_dir}/dev/harbor-district-clerk.json" ||
  fail 'harbor-district-clerk should render the medium profile (2 replicas)'
grep -q '"replicas":1' "${render_dir}/dev/riverbend-permits.json" ||
  fail 'riverbend-permits should render the small profile (1 replica)'
grep -q '"environment":"dev"' "${render_dir}/dev/manifest.json" ||
  fail 'manifest should record the environment'
grep -q '"tenant_count":3' "${render_dir}/dev/manifest.json" ||
  fail 'manifest should count every rendered tenant'

# The bootstrap secret must never reach a rendered file. Only its identifier does.
grep -q '"bootstrap_credential_id"' "${render_dir}/dev/harbor-district-clerk.json" ||
  fail 'rendered file should reference the issued credential by identifier'
if grep -qi 'bootstrap_token' "${render_dir}/dev/harbor-district-clerk.json"; then
  fail 'rendered file must not contain the bootstrap secret'
fi

# 5. Destroy, and confirm the files really are removed rather than orphaned.
echo '--- environments/dev: destroy'
(cd "${environment_dir}" && "${tf}" destroy -auto-approve -input=false \
  -var "output_root=${render_dir}" >/dev/null)
if [ -f "${render_dir}/dev/manifest.json" ]; then
  fail 'destroy left the manifest behind'
fi

echo 'PASS'
