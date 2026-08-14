#!/usr/bin/env bash
# Brings up OpenFGA, runs everything this example claims, and tears it down.
#
# Exits non-zero on the first failure. Requires docker, jq, and Go; the fga CLI is
# optional and its store test is run only if the CLI is on PATH.
set -euo pipefail

cd "$(dirname "$0")"

IMAGE=openfga/openfga:v1.8.4
CONTAINER=rebac-authorization-smoke
PORT=${PORT:-18080}
BASE="http://localhost:${PORT}"

cleanup() {
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

echo "==> starting ${IMAGE} on port ${PORT}"
docker run -d --name "${CONTAINER}" -p "${PORT}:8080" "${IMAGE}" run >/dev/null

echo "==> waiting for the server to report healthy"
for _ in $(seq 1 60); do
  if curl -sf "${BASE}/healthz" >/dev/null; then break; fi
  sleep 1
done
status=$(curl -s -o /dev/null -w '%{http_code}' "${BASE}/healthz")
if [ "${status}" != "200" ]; then
  echo "healthz returned ${status}, want 200" >&2
  exit 1
fi

echo "==> relationship checks (Go)"
OPENFGA_API_URL="${BASE}" go test ./... -count=1

if command -v fga >/dev/null 2>&1; then
  echo "==> store test (fga CLI)"
  fga model test --tests store.fga.yaml
else
  echo "==> skipping the fga CLI store test: fga is not on PATH"
fi

echo "==> the same question over plain HTTP, no SDK"
store=$(curl -sS -X POST "${BASE}/stores" \
  -H 'content-type: application/json' \
  -d '{"name":"smoke"}' | jq -r .id)
model=$(curl -sS -X POST "${BASE}/stores/${store}/authorization-models" \
  -H 'content-type: application/json' \
  -d @model.json | jq -r .authorization_model_id)

status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "${BASE}/stores/${store}/write" \
  -H 'content-type: application/json' \
  -d '{"writes":{"tuple_keys":[
        {"user":"user:dana","relation":"member","object":"organization:parks-department"},
        {"user":"organization:parks-department","relation":"organization","object":"folder:reading-room"},
        {"user":"folder:reading-room","relation":"parent","object":"document:bridge-inspection-2025"}]}}')
if [ "${status}" != "200" ]; then
  echo "write returned ${status}, want 200" >&2
  exit 1
fi

check() {
  curl -sS -X POST "${BASE}/stores/${store}/check" \
    -H 'content-type: application/json' \
    -d "{\"authorization_model_id\":\"${model}\",\"tuple_key\":{\"user\":\"$1\",\"relation\":\"can_view\",\"object\":\"document:bridge-inspection-2025\"}}" |
    jq -r '.allowed'
}

allowed=$(check "user:dana")
if [ "${allowed}" != "true" ]; then
  echo "department member check = ${allowed}, want true" >&2
  exit 1
fi
echo "    user:dana  can_view document:bridge-inspection-2025 -> ${allowed}  (organization membership)"

allowed=$(check "user:quinn")
if [ "${allowed}" != "false" ]; then
  echo "stranger check = ${allowed}, want false" >&2
  exit 1
fi
echo "    user:quinn can_view document:bridge-inspection-2025 -> ${allowed} (no relationship)"

echo "==> all checks passed"
