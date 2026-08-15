#!/usr/bin/env bash
#
# generate-dev-certs.sh
#
# Generates the throwaway TLS material that makes the sidecar unbypassable, into
# ./certs.
#
# The example's claim is that the application is reachable only through the
# policy decision point. Sharing a container network is not that claim: anything
# else on the network can still dial the application directly. Mutual TLS is
# what turns the claim into a property -- the application refuses a connection
# that does not present a certificate signed by this authority, so the only
# caller that can complete a handshake is the proxy.
#
# Nothing under certs/ is ever committed: certs/.gitignore excludes the whole
# directory, and the repository's publication gate fails the build on a committed
# key block. Run this before starting the stack and before validating the Envoy
# configuration -- `--mode validate` opens every file the configuration
# references, so a missing certificate is a validation failure, not a runtime
# one.
#
# What it makes:
#   certs/ca.crt        a local authority. Both ends verify against it and
#                       against nothing else, which is the whole trust boundary.
#   certs/sidecar.crt   the client certificate Envoy presents to the application
#   certs/sidecar.key   its private key
#   certs/upstream.crt  the server certificate the application presents to Envoy
#   certs/upstream.key  its private key
#
# The subject alternative names matter and are checked in both directions: the
# application accepts only a client whose certificate says it is the sidecar, and
# Envoy accepts only a server whose certificate says it is the upstream. A
# certificate signed by the right authority but issued to something else is
# refused, which is what keeps "signed by our CA" from meaning "allowed to be
# anything".
#
# Ninety days: development material that never expires has a way of turning into
# production material.

set -euo pipefail

cd "$(dirname "$0")"

certs_dir="certs"
days=90

# The names the two ends verify each other by. They are DNS names on the
# container network, not hostnames of anything that exists outside this example.
sidecar_san="authorization-sidecar.svc.example"
upstream_san="upstream"

mkdir -p "${certs_dir}"

echo "generate-dev-certs: writing development TLS material to ${certs_dir}/"

# --- Local authority ---------------------------------------------------------

openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout "${certs_dir}/ca.key" \
  -out "${certs_dir}/ca.crt" \
  -days "${days}" \
  -subj "/O=Edgent Code Examples/CN=authorization-sidecar development authority" \
  -addext "basicConstraints=critical,CA:TRUE,pathlen:0" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  2>/dev/null

# issue <name> <extended key usage> <subject alternative name>
issue() {
  local name="$1" usage="$2" san="$3"

  openssl req -newkey rsa:2048 -nodes \
    -keyout "${certs_dir}/${name}.key" \
    -out "${certs_dir}/${name}.csr" \
    -subj "/O=Edgent Code Examples/CN=${san}" \
    2>/dev/null

  cat > "${certs_dir}/${name}.ext" <<EXT
basicConstraints=critical,CA:FALSE
keyUsage=critical,digitalSignature,keyEncipherment
extendedKeyUsage=${usage}
subjectAltName=DNS:${san}
EXT

  openssl x509 -req \
    -in "${certs_dir}/${name}.csr" \
    -CA "${certs_dir}/ca.crt" \
    -CAkey "${certs_dir}/ca.key" \
    -CAcreateserial \
    -out "${certs_dir}/${name}.crt" \
    -days "${days}" \
    -extfile "${certs_dir}/${name}.ext" \
    2>/dev/null

  rm -f "${certs_dir}/${name}.csr" "${certs_dir}/${name}.ext"
}

# The proxy is a client here, so its certificate carries clientAuth and nothing
# else. A certificate that is good for both roles is a certificate that can be
# replayed by whatever it was presented to.
issue "sidecar" "clientAuth" "${sidecar_san}"
issue "upstream" "serverAuth" "${upstream_san}"

rm -f "${certs_dir}/ca.srl"

# Both processes run unprivileged in their containers and read the keys through
# bind mounts.
chmod 0644 "${certs_dir}"/*.crt "${certs_dir}"/*.key

echo "generate-dev-certs: done"
openssl x509 -in "${certs_dir}/sidecar.crt" -noout -subject -ext subjectAltName,extendedKeyUsage
openssl x509 -in "${certs_dir}/upstream.crt" -noout -subject -ext subjectAltName,extendedKeyUsage
