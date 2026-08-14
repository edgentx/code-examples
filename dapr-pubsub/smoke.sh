#!/usr/bin/env bash
# Brings the demo stack up, runs the README's sequence, asserts the outcomes,
# and tears down. Exits non-zero on the first failure.
#
# It uses plain `docker run` on a user-defined network rather than
# `docker compose up`, so it works on machines without the compose plugin. The
# arguments below are the same ones docker-compose.yml passes.
set -euo pipefail

NETWORK=dapr-pubsub-smoke
PUBLISHER_PORT=18300
SUBSCRIBER_PORT=18301
REDIS_PORT=16379
PUBLISHER_SIDECAR_PORT=13500
SUBSCRIBER_SIDECAR_PORT=13501
DAPRD_IMAGE=daprio/daprd:1.15.5
COMPONENTS="$(cd "$(dirname "$0")" && pwd)/components"
CONTAINERS=(smoke-redis smoke-publisher smoke-publisher-dapr smoke-subscriber smoke-subscriber-dapr)

cleanup() {
  docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1 || true
  docker network rm "${NETWORK}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

# expect <description> <expected status> <curl arguments...>
expect() {
  local description=$1 expected=$2
  shift 2
  local actual
  actual=$(curl -s -o /dev/null -w '%{http_code}' "$@")
  [ "${actual}" = "${expected}" ] || fail "${description}: got ${actual}, want ${expected}"
  echo "ok: ${description} (${actual})"
}

wait_for() {
  local url=$1
  for _ in $(seq 1 60); do
    if curl -sf "${url}" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  fail "timed out waiting for ${url}"
}

cleanup
cd "$(dirname "$0")"

echo "--- building"
docker build --quiet --build-arg SERVICE=publisher -t dapr-pubsub-publisher:smoke . >/dev/null
docker build --quiet --build-arg SERVICE=subscriber -t dapr-pubsub-subscriber:smoke . >/dev/null

echo "--- starting"
docker network create "${NETWORK}" >/dev/null
docker run -d --name smoke-redis --network "${NETWORK}" --network-alias redis \
  -p "${REDIS_PORT}:6379" redis:7-alpine >/dev/null

docker run -d --name smoke-publisher --network "${NETWORK}" --network-alias publisher \
  -p "${PUBLISHER_PORT}:8080" \
  -e DAPR_HTTP_ENDPOINT="http://publisher-dapr:3500" \
  -e PUBSUB_COMPONENT=intake-pubsub -e INTAKE_TOPIC=intake-notices \
  dapr-pubsub-publisher:smoke >/dev/null

docker run -d --name smoke-subscriber --network "${NETWORK}" --network-alias subscriber \
  -p "${SUBSCRIBER_PORT}:8080" \
  -e DAPR_HTTP_ENDPOINT="http://subscriber-dapr:3500" \
  -e PUBSUB_COMPONENT=intake-pubsub -e STATE_COMPONENT=intake-statestore \
  -e DEAD_LETTER_TOPIC=intake-notices-parked -e MAX_DELIVERY_ATTEMPTS=3 \
  dapr-pubsub-subscriber:smoke >/dev/null

for role in publisher subscriber; do
  port=${PUBLISHER_SIDECAR_PORT}
  [ "${role}" = subscriber ] && port=${SUBSCRIBER_SIDECAR_PORT}
  docker run -d --name "smoke-${role}-dapr" --network "${NETWORK}" --network-alias "${role}-dapr" \
    -p "${port}:3500" -e REDIS_PASSWORD="" -v "${COMPONENTS}:/components:ro" \
    "${DAPRD_IMAGE}" /daprd \
    "--app-id=intake-${role}" --app-port=8080 "--app-channel-address=${role}" \
    --app-protocol=http --dapr-http-port=3500 --dapr-listen-addresses=0.0.0.0 \
    --resources-path=/components --log-level=info >/dev/null
done

wait_for "http://localhost:${PUBLISHER_PORT}/healthz"
wait_for "http://localhost:${SUBSCRIBER_PORT}/healthz"
wait_for "http://localhost:${PUBLISHER_SIDECAR_PORT}/v1.0/healthz"
wait_for "http://localhost:${SUBSCRIBER_SIDECAR_PORT}/v1.0/healthz"

echo "--- publishing"
expect "a valid notice is accepted" 202 \
  -X POST "http://localhost:${PUBLISHER_PORT}/intake" -H 'Content-Type: application/json' \
  -d '{"noticeId":"SMOKE-1","agencyCode":"DPR","seriesCode":"RS-100","pageCount":12}'
expect "an invalid notice is rejected before the topic sees it" 400 \
  -X POST "http://localhost:${PUBLISHER_PORT}/intake" -H 'Content-Type: application/json' \
  -d '{"noticeId":"SMOKE-2","agencyCode":"DPR","seriesCode":"","pageCount":12}'
expect "a poison notice is accepted for delivery" 202 \
  -X POST "http://localhost:${PUBLISHER_PORT}/intake" -H 'Content-Type: application/json' \
  -d '{"noticeId":"SMOKE-9","agencyCode":"DPR","seriesCode":"RS-999","pageCount":40}'

echo "--- waiting for the delivery budget to be spent and the message parked"
wait_for "http://localhost:${SUBSCRIBER_PORT}/parked/SMOKE-9"

parked=$(curl -s "http://localhost:${SUBSCRIBER_PORT}/parked/SMOKE-9")
echo "${parked}" | grep -q '"attempts":3' || fail "parked record does not show the spent budget: ${parked}"
echo "${parked}" | grep -q 'retention catalog' || fail "parked record does not carry the reason: ${parked}"
echo "ok: poison message parked with reason and attempt count"

expect "a processed notice is not in the parked queue" 404 \
  "http://localhost:${SUBSCRIBER_PORT}/parked/SMOKE-1"

echo "--- subscriber log"
docker logs smoke-subscriber 2>&1 | tail -n 10

echo "PASS"
