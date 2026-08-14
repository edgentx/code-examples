#!/usr/bin/env bash
#
# check-no-committed-secrets.sh
#
# Publication gate for this repository. Every example here is public, so the
# repository must never carry a credential, a customer identifier, or the address
# of an internal system. This script scans the working tree (tracked files only)
# and fails the build on the first match.
#
# Usage:
#   scripts/check-no-committed-secrets.sh [path ...]
#
# With no arguments it scans everything git tracks from the repository root.

set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

self_path="scripts/check-no-committed-secrets.sh"
findings=0

# Files the scan never inspects: this script (it necessarily contains the
# patterns), binary assets, and vendored dependency trees.
is_scannable() {
  local path="$1"
  case "${path}" in
    "${self_path}") return 1 ;;
    *node_modules/*|*/vendor/*|*.png|*.jpg|*.jpeg|*.gif|*.pdf|*.ico|*.woff|*.woff2) return 1 ;;
  esac
  [ -f "${path}" ] || return 1
  # Skip anything git considers binary.
  grep -Iq . "${path}" 2>/dev/null || return 1
  return 0
}

# report <rule> <description> <file:line:content>
report() {
  printf 'FAIL [%s] %s\n      %s\n' "$1" "$2" "$3"
  findings=$((findings + 1))
}

# scan <rule> <description> <extended-regex> [file ...]
scan() {
  local rule="$1" description="$2" pattern="$3"
  shift 3
  local hit
  while IFS= read -r hit; do
    [ -n "${hit}" ] || continue
    report "${rule}" "${description}" "${hit}"
  done < <(grep -nEI --with-filename "${pattern}" "$@" 2>/dev/null || true)
}

mapfile -t candidates < <(
  if [ "$#" -gt 0 ]; then
    git ls-files -- "$@"
  else
    git ls-files
  fi
)

files=()
for candidate in "${candidates[@]}"; do
  if is_scannable "${candidate}"; then
    files+=("${candidate}")
  fi
done

if [ "${#files[@]}" -eq 0 ]; then
  echo "check-no-committed-secrets: nothing to scan"
  exit 0
fi

echo "check-no-committed-secrets: scanning ${#files[@]} tracked text files"

# --- Credentials -------------------------------------------------------------

scan "aws-access-key" \
  "AWS access key identifier" \
  '(A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z0-9]{16}' "${files[@]}"

scan "github-token" \
  "GitHub personal access, OAuth, or app token" \
  '(gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{30,})' "${files[@]}"

scan "slack-token" \
  "Slack API token" \
  'xox[abprs]-[A-Za-z0-9-]{10,}' "${files[@]}"

scan "private-key" \
  "PEM private key block" \
  'BEGIN ([A-Z]+ )?PRIVATE KEY' "${files[@]}"

scan "bearer-literal" \
  "hard-coded bearer token" \
  '[Aa]uthorization: *[Bb]earer +[A-Za-z0-9._~+/-]{20,}' "${files[@]}"

scan "jwt" \
  "JSON Web Token literal" \
  'eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}' "${files[@]}"

# Assigned secrets. Obvious non-values -- environment interpolation, "changeme",
# "example", "REPLACE_ME" and friends -- stay allowed so example configuration
# remains readable without weakening the rule for real literals.
assigned_secret_pattern='(password|passwd|secret|api[_-]?key|access[_-]?token|client[_-]?secret)[[:space:]]*[:=][[:space:]]*["'"'"']?[A-Za-z0-9/+_.-]{12,}'
placeholder_pattern='(changeme|change-me|placeholder|replace[_-]?me|redacted|example|sample|dummy|not-?a-?secret|\$\{|\{\{|<[A-Za-z_-]+>)'
while IFS= read -r hit; do
  [ -n "${hit}" ] || continue
  report "assigned-secret" "assigned password, secret, or API key literal" "${hit}"
done < <(
  grep -nEI --with-filename "${assigned_secret_pattern}" "${files[@]}" 2>/dev/null |
    grep -vEi "${placeholder_pattern}" || true
)

# --- Internal systems --------------------------------------------------------

scan "private-ip" \
  "RFC 1918 address literal" \
  '(^|[^0-9.])(10\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|192\.168\.[0-9]{1,3}\.[0-9]{1,3}|172\.(1[6-9]|2[0-9]|3[01])\.[0-9]{1,3}\.[0-9]{1,3})([^0-9.]|$)' \
  "${files[@]}"

scan "cluster-dns" \
  "in-cluster service address" \
  '[A-Za-z0-9-]+\.[A-Za-z0-9-]+\.svc(\.cluster\.local)?' "${files[@]}"

scan "internal-hostname" \
  "internal or private hostname" \
  '\b[A-Za-z0-9-]+\.(internal|intranet|corp|lan|home|localdomain)\b' "${files[@]}"

scan "internal-domain" \
  "non-public Edgent or VForce host" \
  '\b[A-Za-z0-9-]+\.(vforce360\.ai|edgentllc\.com)\b' "${files[@]}"

# --- Result ------------------------------------------------------------------

if [ "${findings}" -gt 0 ]; then
  printf '\ncheck-no-committed-secrets: %d finding(s). Nothing here may be published.\n' "${findings}"
  echo 'If a match is a false positive, narrow the value in the example rather than widening the gate.'
  exit 1
fi

echo "check-no-committed-secrets: clean"
