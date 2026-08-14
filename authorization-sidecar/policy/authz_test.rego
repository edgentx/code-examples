# Policy tests. Run with OPA v1.4.2 or newer:
#
#   opa test --verbose policy/
#
# Every test builds the ext_authz check input Envoy would send and asserts the
# decision. The deny cases matter more than the allow cases: a policy that only
# proves it lets the right people in has proved half of nothing.

package envoy.authz_test

import rego.v1

import data.envoy.authz

# check_input builds the attributes Envoy sends on an ext_authz call. Header
# names are lowercased, as Envoy delivers them.
check_input(method, path, headers) := {"attributes": {"request": {"http": {
	"method": method,
	"path": path,
	"headers": headers,
}}}}

reader_credential := {"authorization": "Bearer t-rosalind-reader"}

owner_credential := {"authorization": "Bearer t-omar-owner"}

auditor_credential := {"authorization": "Bearer t-avery-auditor"}

# ---------------------------------------------------------------------------
# Allow: the open set
# ---------------------------------------------------------------------------

test_allow_health_with_no_credential if {
	authz.allow with input as check_input("GET", "/health", {})
}

test_allow_readiness_with_no_credential if {
	authz.allow with input as check_input("GET", "/ready", {})
}

# ---------------------------------------------------------------------------
# Allow: the authenticated rules
# ---------------------------------------------------------------------------

test_allow_reader_listing_documents if {
	authz.allow with input as check_input("GET", "/api/documents", reader_credential)
}

test_allow_reader_fetching_one_document if {
	authz.allow with input as check_input("GET", "/api/documents/doc-1001", reader_credential)
}

test_allow_owner_deleting_a_document if {
	authz.allow with input as check_input("DELETE", "/api/documents/doc-1001", owner_credential)
}

# An owner also holds the reader role, so read access is not lost by being an
# owner. This is the case a role model usually gets wrong in the other
# direction.
test_allow_owner_reading_a_document if {
	authz.allow with input as check_input("GET", "/api/documents/doc-1001", owner_credential)
}

# ---------------------------------------------------------------------------
# Deny
# ---------------------------------------------------------------------------

test_deny_request_with_no_identity_at_all if {
	not authz.allow with input as check_input("GET", "/api/documents", {})
}

# A real, verifiable identity that simply does not hold the role the route
# requires. Authentication is not authorization.
test_deny_valid_identity_without_the_required_role if {
	not authz.allow with input as check_input("GET", "/api/documents", auditor_credential)
}

# The separation-of-duty case: a reader token on a destructive route.
test_deny_read_only_identity_attempting_a_delete if {
	not authz.allow with input as check_input("DELETE", "/api/documents/doc-1001", reader_credential)
}

# Outside the closed set: four path segments, so no rule matches and the default
# denial stands. Nothing had to be written to block this path.
test_deny_path_outside_the_closed_set if {
	not authz.allow with input as check_input("GET", "/api/documents/doc-1001/audit-trail", owner_credential)
}

test_deny_unrelated_path_outside_the_closed_set if {
	not authz.allow with input as check_input("GET", "/admin/console", owner_credential)
}

# A query string cannot smuggle a request past an exact-path rule.
test_deny_public_path_extended_with_a_suffix if {
	not authz.allow with input as check_input("GET", "/health/../api/documents", {})
}

# A token that is not in the directory is not an identity.
test_deny_unrecognized_bearer_token if {
	not authz.allow with input as check_input("GET", "/api/documents", {"authorization": "Bearer t-not-issued"})
}

# ---------------------------------------------------------------------------
# FORGED IDENTITY HEADERS
#
# The attack this whole design exists to defeat: a caller on the network sets
# the identity headers itself and hopes something downstream believes them.
# These requests carry no credential at all -- only headers the client typed.
# The policy must not read them, and the proof is that the decision is identical
# to the decision for a request with no headers whatsoever.
# ---------------------------------------------------------------------------

forged_identity_headers := {
	"x-user-id": "owner-omar",
	"x-user-roles": "owner,reader,admin",
	"x-authz-subject": "owner-omar",
	"x-authz-roles": "owner",
	"x-authz-decision": "allow",
}

test_deny_forged_identity_headers_on_a_read if {
	not authz.allow with input as check_input("GET", "/api/documents", forged_identity_headers)
}

test_deny_forged_identity_headers_on_a_delete if {
	not authz.allow with input as check_input("DELETE", "/api/documents/doc-1001", forged_identity_headers)
}

# The forged headers make no difference whatsoever: same denial as sending
# nothing. If this ever failed, some rule had started reading a client header.
test_forged_identity_headers_change_nothing if {
	forged := authz.result with input as check_input("GET", "/api/documents", forged_identity_headers)
	bare := authz.result with input as check_input("GET", "/api/documents", {})
	forged == bare
}

# A reader token plus forged headers claiming the owner role still cannot
# delete. The credential decides; the header is ignored.
test_deny_forged_owner_headers_over_a_reader_credential if {
	headers := object.union(reader_credential, forged_identity_headers)
	not authz.allow with input as check_input("DELETE", "/api/documents/doc-1001", headers)
}

# And on a route the reader legitimately holds, the stamped identity is the
# verified one, not the forged one. Envoy sets these headers on the forwarded
# request, so this overwrites what the client sent.
test_forged_identity_header_is_overwritten_by_the_verified_subject if {
	headers := object.union(reader_credential, forged_identity_headers)
	decision := authz.result with input as check_input("GET", "/api/documents", headers)
	decision.allowed == true
	decision.headers["x-authz-subject"] == "reader-rosalind"
	decision.headers["x-authz-roles"] == "reader"
}

# ---------------------------------------------------------------------------
# The decision Envoy receives
# ---------------------------------------------------------------------------

test_deny_result_is_an_explicit_403 if {
	decision := authz.result with input as check_input("GET", "/api/documents", {})
	decision.allowed == false
	decision.http_status == 403
}

test_allow_result_stamps_the_verified_roles if {
	decision := authz.result with input as check_input("DELETE", "/api/documents/doc-1001", owner_credential)
	decision.allowed == true
	decision.headers["x-authz-subject"] == "owner-omar"
	decision.headers["x-authz-roles"] == "owner,reader"
	decision.headers["x-authz-decision"] == "allow"
}

# A public route is stamped as well, with a subject that grants nothing, so the
# upstream can never be handed a leftover client value in these header names.
test_public_route_is_stamped_anonymous if {
	decision := authz.result with input as check_input("GET", "/health", forged_identity_headers)
	decision.allowed == true
	decision.headers["x-authz-subject"] == "anonymous"
	decision.headers["x-authz-roles"] == "none"
	decision.headers["x-authz-decision"] == "allow-public"
}
