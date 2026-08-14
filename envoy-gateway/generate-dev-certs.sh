#!/usr/bin/env bash
#
# generate-dev-certs.sh
#
# Generates the throwaway TLS material the gateway listener needs, into ./certs.
#
# Nothing under certs/ is ever committed: certs/.gitignore excludes the whole
# directory, and the repository's publication gate fails the build on a
# committed key block. Run this before starting the stack and before validating
# the Envoy configuration -- `--mode validate` opens every file the
# configuration references, so a missing certificate is a validation failure,
# not a runtime one.
#
# What it makes:
#   certs/ca.crt      a local certificate authority, so curl can verify the
#                     gateway with --cacert instead of --insecure. Verifying
#                     against a named authority is the habit worth keeping;
#                     "-k" teaches the opposite one.
#   certs/server.crt  the gateway's leaf certificate, signed by that authority
#   certs/server.key  the matching private key
#
# The leaf is valid for localhost and for the compose service name, so the same
# certificate works whether the caller is on the host or on the docker network.
# It expires in 90 days: development material that never expires has a way of
# turning into production material.

set -euo pipefail

cd "$(dirname "$0")"

certs_dir="certs"
days=90

mkdir -p "${certs_dir}"

echo "generate-dev-certs: writing development TLS material to ${certs_dir}/"

# --- Local authority ---------------------------------------------------------

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${certs_dir}/ca.key" \
  -out "${certs_dir}/ca.crt" \
  -days "${days}" \
  -subj "/O=Edgent Code Examples/CN=envoy-gateway development authority" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  2>/dev/null

# --- Gateway leaf ------------------------------------------------------------

openssl req -newkey rsa:2048 -nodes \
  -keyout "${certs_dir}/server.key" \
  -out "${certs_dir}/server.csr" \
  -subj "/O=Edgent Code Examples/CN=localhost" \
  2>/dev/null

cat > "${certs_dir}/server.ext" <<'EXT'
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=serverAuth
subjectAltName=DNS:localhost,DNS:gateway,IP:127.0.0.1
EXT

openssl x509 -req \
  -in "${certs_dir}/server.csr" \
  -CA "${certs_dir}/ca.crt" \
  -CAkey "${certs_dir}/ca.key" \
  -CAcreateserial \
  -out "${certs_dir}/server.crt" \
  -days "${days}" \
  -extfile "${certs_dir}/server.ext" \
  2>/dev/null

rm -f "${certs_dir}/server.csr" "${certs_dir}/server.ext" "${certs_dir}/ca.srl"

# Envoy runs unprivileged in the container and reads the key through a bind
# mount, so the key stays owner-readable but the certificates stay world
# readable.
chmod 0644 "${certs_dir}/ca.crt" "${certs_dir}/server.crt"
chmod 0644 "${certs_dir}/server.key"

echo "generate-dev-certs: done"
openssl x509 -in "${certs_dir}/server.crt" -noout -subject -dates -ext subjectAltName
