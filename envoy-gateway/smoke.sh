#!/usr/bin/env bash
#
# smoke.sh
#
# Brings the stack up, runs the README's demonstrations as assertions, and tears
# it down. Exits non-zero on the first failure, so it is safe to gate a merge on.
#
# It asserts the four claims the example makes:
#   1. a request routed over TLS reaches service-a with the prefix rewritten
#   2. identity headers a client forges do not reach the upstream
#   3. the slow route is cut off by the gateway with a 504
#   4. the access log is JSON with the named fields
#
# This uses plain `docker run` rather than `docker compose` so it works on any
# machine with a docker daemon, including build images without the compose
# plugin. docker-compose.yml describes the same three containers on the same
# network with the same ports.

set -euo pipefail

cd "$(dirname "$0")"

network="envoy-gateway-smoke"
gateway="envoy-gateway-smoke-gw"
upstream_a="envoy-gateway-smoke-a"
upstream_b="envoy-gateway-smoke-b"

envoy_image="envoyproxy/envoy:v1.34-latest"
image_a="envoy-gateway-smoke/service-a"
image_b="envoy-gateway-smoke/service-b"

https="https://localhost:18443"
http="http://localhost:18100"

failures=0

fail() {
  printf 'FAIL %s\n' "$1" >&2
  failures=$((failures + 1))
}

pass() {
  printf 'ok   %s\n' "$1"
}

teardown() {
  docker rm -f "${gateway}" "${upstream_a}" "${upstream_b}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
}
trap teardown EXIT

# --- Bring the stack up ------------------------------------------------------

echo '--- generating development certificates'
./generate-dev-certs.sh >/dev/null

echo '--- building upstream images'
docker build --quiet --build-arg SERVICE=service-a -t "${image_a}" . >/dev/null
docker build --quiet --build-arg SERVICE=service-b -t "${image_b}" . >/dev/null

echo '--- starting the stack'
teardown
docker network create --subnet 203.0.113.0/24 "${network}" >/dev/null

docker run -d --name "${upstream_a}" --network "${network}" --network-alias service-a \
  -p 127.0.0.1:18101:8080 "${image_a}" >/dev/null
docker run -d --name "${upstream_b}" --network "${network}" --network-alias service-b \
  -p 127.0.0.1:18102:8080 -e SLOW_FOR=5s "${image_b}" >/dev/null
docker run -d --name "${gateway}" --network "${network}" --network-alias gateway \
  -v "${PWD}:/workspace:ro" -w /workspace \
  -p 127.0.0.1:18100:8080 -p 127.0.0.1:18443:8443 \
  "${envoy_image}" -c envoy-gateway.yaml --file-flush-interval-msec 200 >/dev/null

echo '--- waiting for the gateway'
ready=0
for _ in $(seq 1 60); do
  if curl -sf --cacert certs/ca.crt "${https}/api/a/healthz" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "${ready}" -ne 1 ]; then
  echo 'the gateway never answered; recent gateway output follows' >&2
  docker logs "${gateway}" 2>&1 | tail -30 >&2
  exit 1
fi

# --- 1. A routed request reaches service-a with the prefix rewritten ---------

routed=$(curl -sS --cacert certs/ca.crt "${https}/api/a/records/42")
if [ "$(printf '%s' "${routed}" | grep -c '"service": "service-a"')" -eq 1 ]; then
  pass 'routed request reached service-a'
else
  fail "routed request did not reach service-a: ${routed}"
fi
if [ "$(printf '%s' "${routed}" | grep -c '"path": "/records/42"')" -eq 1 ]; then
  pass 'the /api/a prefix was rewritten away'
else
  fail "the prefix was not rewritten: ${routed}"
fi

# --- 2. Forged identity headers do not survive the edge ----------------------

forged=$(curl -sS --cacert certs/ca.crt \
  -H 'x-user-id: 900001' \
  -H 'x-user-roles: records-administrator' \
  -H 'x-forwarded-client-cert: By=spoofed;Subject="CN=someone-else"' \
  -H 'x-request-id: forged-correlation-id' \
  "${https}/api/a/records/42")

for header in x-user-id x-user-roles x-forwarded-client-cert forged-correlation-id; do
  if printf '%s' "${forged}" | grep -q "${header}"; then
    fail "the edge forwarded ${header} to the upstream"
  else
    pass "the edge stripped ${header}"
  fi
done

if printf '%s' "${forged}" | grep -q '"x-edge-gateway": "envoy-edge"'; then
  pass 'the edge stamped x-edge-gateway'
else
  fail "the edge did not stamp x-edge-gateway: ${forged}"
fi

# --- 3. The slow route is cut off by the gateway -----------------------------

slow_status=$(curl -sS -o /dev/null -w '%{http_code}' --cacert certs/ca.crt "${https}/api/b/slow")
if [ "${slow_status}" = "504" ]; then
  pass 'the slow route returned 504'
else
  fail "the slow route returned ${slow_status}, want 504"
fi

# --- 4. Cleartext redirects, and the access log is JSON with named fields ----

redirect_status=$(curl -sS -o /dev/null -w '%{http_code}' "${http}/api/a/records/42")
if [ "${redirect_status}" = "301" ]; then
  pass 'the cleartext listener redirected to HTTPS'
else
  fail "the cleartext listener returned ${redirect_status}, want 301"
fi

# Envoy buffers its access log; the stack is started with a short flush
# interval, but give the line a moment to appear rather than racing it.
log_line=""
for _ in $(seq 1 20); do
  log_line=$(docker logs "${gateway}" 2>&1 | grep '"path":"/api/b/slow"' | tail -1 || true)
  [ -n "${log_line}" ] && break
  sleep 1
done
if printf '%s' "${log_line}" | grep -q '"response_flags":"URX,UT"'; then
  pass 'the access log recorded the timeout with an upstream-timeout flag'
else
  fail "no timeout flag in the access log: ${log_line}"
fi

# --- Result ------------------------------------------------------------------

if [ "${failures}" -gt 0 ]; then
  printf '\nsmoke: %d failure(s)\n' "${failures}" >&2
  exit 1
fi

echo
echo 'smoke: the edge routed, stripped, stamped, timed out, and logged as configured'
