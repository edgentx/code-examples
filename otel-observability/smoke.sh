#!/usr/bin/env bash
# Brings the demo stack up, runs the README's requests, asserts that one request
# produced spans from both services under one trace id, and tears the stack down.
# Exits non-zero on any failure.
#
# This uses plain `docker run` on a user-defined network rather than the compose
# plugin, so it works anywhere a Docker daemon is reachable. It mirrors
# docker-compose.yml exactly: same images, same environment, same ports.
set -euo pipefail

cd "$(dirname "$0")"

NETWORK=otel-observability-smoke
COLLECTOR=otel-observability-smoke-collector
RECORDS=otel-observability-smoke-records
FRONTDESK=otel-observability-smoke-frontdesk

COLLECTOR_IMAGE=otel/opentelemetry-collector-contrib:0.140.0
CASE_OK=CASE-2026-0203
CASE_MISSING=CASE-2026-9999

# The collector batches for 5s before exporting; give it room.
EXPORT_WAIT=12

cleanup() {
  docker rm -f "$FRONTDESK" "$RECORDS" "$COLLECTOR" >/dev/null 2>&1 || true
  docker network rm "$NETWORK" >/dev/null 2>&1 || true
}
trap cleanup EXIT

LOGS=$(mktemp -d)
cleanup_logs() { rm -rf "$LOGS"; }
trap 'cleanup; cleanup_logs' EXIT

fail() {
  echo "FAIL: $*" >&2
  [ -f "$LOGS/collector.log" ] && tail -40 "$LOGS/collector.log" >&2
  exit 1
}

echo "==> building images"
docker build --quiet --build-arg SERVICE=frontdesk -t otel-observability-frontdesk:smoke . >/dev/null
docker build --quiet --build-arg SERVICE=records -t otel-observability-records:smoke . >/dev/null

echo "==> starting stack"
cleanup
docker network create "$NETWORK" >/dev/null

docker run -d --name "$COLLECTOR" --network "$NETWORK" --network-alias collector \
  -v "$PWD/otel-collector.yaml:/etc/otelcol/config.yaml:ro" \
  -p 14317:4317 -p 14318:4318 \
  "$COLLECTOR_IMAGE" --config=/etc/otelcol/config.yaml >/dev/null

docker run -d --name "$RECORDS" --network "$NETWORK" --network-alias records \
  -e RECORDS_ADDR=:8201 \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318 \
  -p 18201:8201 otel-observability-records:smoke >/dev/null

docker run -d --name "$FRONTDESK" --network "$NETWORK" --network-alias frontdesk \
  -e FRONTDESK_ADDR=:8200 \
  -e RECORDS_BASE_URL=http://records:8201 \
  -e OTEL_EXPORTER_OTLP_ENDPOINT=http://collector:4318 \
  -p 18200:8200 otel-observability-frontdesk:smoke >/dev/null

echo "==> waiting for the front desk"
ready=false
for _ in $(seq 1 60); do
  if curl -sf -o /dev/null "http://localhost:18200/requests/${CASE_OK}"; then
    ready=true
    break
  fi
  sleep 1
done
[ "$ready" = true ] || fail "front desk never answered on port 18200"

echo "==> the slow case file answers 200"
status=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:18200/requests/${CASE_OK}")
[ "$status" = "200" ] || fail "expected 200 for ${CASE_OK}, got ${status}"

echo "==> a case file that does not exist answers 404"
status=$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:18200/requests/${CASE_MISSING}")
[ "$status" = "404" ] || fail "expected 404 for ${CASE_MISSING}, got ${status}"

echo "==> waiting ${EXPORT_WAIT}s for the collector to flush a batch"
sleep "$EXPORT_WAIT"

# Capture each log to a file first. Piping `docker logs` straight into `grep -q`
# looks tidier but races: grep exits on the first match, docker takes SIGPIPE,
# and under `set -o pipefail` the assertion fails for reasons that have nothing
# to do with what was being asserted.
docker logs "$FRONTDESK" >"$LOGS/frontdesk.log" 2>&1
docker logs "$RECORDS" >"$LOGS/records.log" 2>&1
docker logs "$COLLECTOR" >"$LOGS/collector.log" 2>&1

# The front desk logged a trace id for the slow case. That is the id a support
# desk would be handed, so it is the id everything else has to line up with.
trace_id=$(grep -m1 -o "\"trace_id\":\"[0-9a-f]\{32\}\"" "$LOGS/frontdesk.log" | cut -d'"' -f4)
[ -n "$trace_id" ] || fail "no trace id in the front desk logs"
echo "==> trace id ${trace_id}"

count() { grep -c "$1" "$2" || true; }

[ "$(count "$trace_id" "$LOGS/records.log")" -ge 1 ] \
  || fail "the records service logged nothing under trace ${trace_id}: propagation is broken"

span_count=$(grep -c -E "^ +Trace ID +: ${trace_id}\$" "$LOGS/collector.log" || true)
[ "$span_count" -ge 5 ] \
  || fail "collector received ${span_count} spans for trace ${trace_id}, want at least 5"

[ "$(count "service.name: Str(frontdesk)" "$LOGS/collector.log")" -ge 1 ] \
  || fail "no spans from the front desk reached the collector"
[ "$(count "service.name: Str(records)" "$LOGS/collector.log")" -ge 1 ] \
  || fail "no spans from the records office reached the collector"

echo "PASS: one request, one trace (${trace_id}), ${span_count} spans across both services"
