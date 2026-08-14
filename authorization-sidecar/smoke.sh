#!/usr/bin/env bash
#
# smoke.sh -- bring the stack up, prove the authorization boundary, tear it down.
#
# Every assertion here is a claim this example makes in its README, checked
# against a running proxy rather than against a mock. The last group is the one
# worth reading: it stops the policy engine and shows that reads stop with it.
# A sidecar that keeps serving when its decision point is gone is not an
# authorization control, and this is the only way to find that out.
#
# Usage:  ./smoke.sh
# Exit:   0 if every assertion held, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")" || exit 1

GATEWAY="http://localhost:18000"
UPSTREAM="http://localhost:18080"

READER="Authorization: Bearer t-rosalind-reader"
OWNER="Authorization: Bearer t-omar-owner"
AUDITOR="Authorization: Bearer t-avery-auditor"

# Headers a client sets on its own request, hoping something downstream believes
# them. They are worth exactly nothing, and that is the assertion.
FORGED_SUBJECT="X-Authz-Subject: owner-omar"
FORGED_ROLES="X-Authz-Roles: owner,reader"
FORGED_USER="X-User-Id: owner-omar"

failures=0

pass() { printf '  ok   %s\n' "$1"; }

fail() {
  printf '  FAIL %s\n' "$1"
  failures=$((failures + 1))
}

# expect <description> <expected status> <curl arguments...>
expect() {
  local description="$1" want="$2"
  shift 2
  local got
  got="$(curl -s -o /dev/null -w '%{http_code}' "$@")"
  if [ "${got}" = "${want}" ]; then
    pass "${description} (${got})"
  else
    fail "${description}: expected ${want}, got ${got}"
  fi
}

# expect_body <description> <substring> <curl arguments...>
expect_body() {
  local description="$1" want="$2"
  shift 2
  local body
  body="$(curl -s "$@")"
  case "${body}" in
    *"${want}"*) pass "${description}" ;;
    *) fail "${description}: response did not contain '${want}'
       ${body}" ;;
  esac
}

# wait_for_status <url> <expected status> [curl arguments...]
wait_for_status() {
  local url="$1" want="$2"
  shift 2
  local got
  for _ in $(seq 1 60); do
    got="$(curl -s -o /dev/null -w '%{http_code}' "$@" "${url}" 2>/dev/null)"
    if [ "${got}" = "${want}" ]; then
      return 0
    fi
    sleep 1
  done
  return 1
}

teardown() {
  echo
  echo '== tearing down'
  docker compose down -v --remove-orphans >/dev/null 2>&1
}

if ! docker compose version >/dev/null 2>&1; then
  echo 'smoke.sh needs the docker compose plugin (docker compose version).' >&2
  exit 1
fi

trap teardown EXIT

echo '== bringing the stack up'
if ! docker compose up -d --build >/dev/null; then
  echo 'compose failed to start the stack' >&2
  exit 1
fi

if ! wait_for_status "${GATEWAY}/health" 200; then
  echo 'the proxy never became ready' >&2
  docker compose logs --tail 40 >&2
  exit 1
fi

echo
echo '== the open set: no credential required, nothing sensitive behind it'
expect 'health is served unauthenticated' 200 "${GATEWAY}/health"
expect 'readiness is served unauthenticated' 200 "${GATEWAY}/ready"
expect_body 'an unauthenticated caller is stamped anonymous' '"subject":"anonymous"' "${GATEWAY}/health"

echo
echo '== everything else is denied by default'
expect 'listing documents without identity' 403 "${GATEWAY}/api/documents"
expect 'fetching a document without identity' 403 "${GATEWAY}/api/documents/doc-1002"
expect 'a path outside the closed set' 403 -H "${OWNER}" "${GATEWAY}/api/documents/doc-1002/audit-trail"
expect 'an unissued bearer token' 403 -H 'Authorization: Bearer t-not-issued' "${GATEWAY}/api/documents"

echo
echo '== verified identity, and only the role the route requires'
expect 'a reader listing documents' 200 -H "${READER}" "${GATEWAY}/api/documents"
expect 'a reader fetching one document' 200 -H "${READER}" "${GATEWAY}/api/documents/doc-1002"
expect 'a reader attempting a delete' 403 -X DELETE -H "${READER}" "${GATEWAY}/api/documents/doc-1002"
expect 'an auditor, authenticated but without the reader role' 403 -H "${AUDITOR}" "${GATEWAY}/api/documents"
expect 'an owner deleting a document' 200 -X DELETE -H "${OWNER}" "${GATEWAY}/api/documents/doc-1001"
expect_body 'the verified subject reaches the application' '"subject":"reader-rosalind"' \
  -H "${READER}" "${GATEWAY}/api/documents"

echo
echo '== forged identity headers buy nothing'
expect 'forged identity headers with no credential' 403 \
  -H "${FORGED_SUBJECT}" -H "${FORGED_ROLES}" -H "${FORGED_USER}" "${GATEWAY}/api/documents"
expect 'forged owner headers on a delete, with no credential' 403 \
  -H "${FORGED_SUBJECT}" -H "${FORGED_ROLES}" -H "${FORGED_USER}" -X DELETE "${GATEWAY}/api/documents/doc-1002"
expect 'forged owner headers over a reader credential, on a delete' 403 \
  -H "${READER}" -H "${FORGED_SUBJECT}" -H "${FORGED_ROLES}" -X DELETE "${GATEWAY}/api/documents/doc-1002"
# The forged value is overwritten, not merged: the application reads the subject
# the policy verified, never the one the client typed.
expect_body 'the forged subject is overwritten by the verified one' '"subject":"reader-rosalind"' \
  -H "${READER}" -H "${FORGED_SUBJECT}" -H "${FORGED_ROLES}" "${GATEWAY}/api/documents"
expect_body 'the forged header survives only as an echoed, unused field' '"client_supplied_user_id":"owner-omar"' \
  -H "${READER}" -H "${FORGED_USER}" "${GATEWAY}/api/documents"

echo
echo '== the application itself has no opinion, which is why the boundary matters'
expect 'reached directly, the application serves an unauthenticated read' 200 "${UPSTREAM}/api/documents"

echo
echo '== FAIL CLOSED: stop the policy engine and the door must shut'
docker compose stop opa >/dev/null 2>&1
# Envoy needs a moment to notice the decision point is gone.
sleep 2
expect 'a valid reader credential, with no policy engine to consult' 403 -H "${READER}" "${GATEWAY}/api/documents"
expect 'an owner credential, with no policy engine to consult' 403 -H "${OWNER}" "${GATEWAY}/api/documents"
# Even the open set closes. The proxy is not deciding that health is public --
# the policy is, and the policy is unreachable.
expect 'even the unauthenticated health route, with no policy engine' 403 "${GATEWAY}/health"

echo
echo '== and it opens again when the policy engine returns'
docker compose start opa >/dev/null 2>&1
if wait_for_status "${GATEWAY}/api/documents" 200 -H "${READER}"; then
  pass 'reads resume once the policy engine is back'
else
  fail 'reads did not resume after the policy engine restarted'
fi

echo
if [ "${failures}" -ne 0 ]; then
  printf '%d assertion(s) failed\n' "${failures}"
  exit 1
fi
echo 'all assertions held'
