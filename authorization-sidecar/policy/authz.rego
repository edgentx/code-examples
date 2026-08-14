# Authorization policy for the document service, evaluated by Open Policy Agent
# on behalf of Envoy's ext_authz filter. Envoy asks this policy about every
# request before it forwards anything upstream.
#
# The policy is built by the closed-set method, and the order of this file is
# the method:
#
#   1. Deny by default. `default allow := false` is the first rule, so a request
#      that matches nothing is refused. A policy written the other way around --
#      allow, with a list of denials -- fails open on every route somebody
#      forgets to add, and "somebody forgot" is how most authorization findings
#      read.
#   2. Enumerate the open set: the handful of requests that may proceed with no
#      credential at all. Health and readiness, nothing else, and they are
#      matched on method AND exact path so that /health/../api/documents is not
#      quietly a member.
#   3. Enumerate the authenticated rules, each naming the role it requires.
#   4. Everything else is already denied by step 1. There is no `deny` rule to
#      keep in sync, which is the point.
#
# Identity comes from exactly one place: the bearer token on the request, looked
# up in a directory. It never comes from a request header the client could have
# written. See "forged identity" below -- that distinction is the difference
# between an authorization control and a suggestion.

package envoy.authz

import rego.v1

# ---------------------------------------------------------------------------
# 1. Deny by default
# ---------------------------------------------------------------------------

default allow := false

# ---------------------------------------------------------------------------
# Request shape. Envoy's ext_authz check carries the request attributes; header
# names arrive lowercased.
# ---------------------------------------------------------------------------

http_request := input.attributes.request.http

method := http_request.method

# The query string is not part of the routing decision, and leaving it attached
# would make every exact-path comparison below trivially bypassable by
# appending "?".
path := split(http_request.path, "?")[0]

# ---------------------------------------------------------------------------
# Identity. THE ONLY SOURCE.
#
# `subject` is derived from the bearer token and from nothing else. Note what
# this policy never reads: x-user-id, x-authz-subject, x-authz-roles, or any
# other header a client can set on its own request. A header is not a
# credential. If this policy trusted one, any caller on the network could grant
# themselves any role by typing it, and every rule below would be decoration.
#
# The directory here is a static demo table so the example runs with no identity
# provider attached. In a real deployment the token is a signed JWT and this
# block becomes a signature verification against the provider's published keys,
# with the roles read out of the verified claims. What does not change is that
# `subject` is undefined unless a credential was verified -- and every
# authenticated rule below depends on `subject`, so an unverifiable request
# cannot satisfy any of them.
# ---------------------------------------------------------------------------

demo_directory := {
	"t-rosalind-reader": {"id": "reader-rosalind", "roles": {"reader"}},
	"t-omar-owner": {"id": "owner-omar", "roles": {"owner", "reader"}},
	"t-avery-auditor": {"id": "auditor-avery", "roles": {"auditor"}},
}

bearer_token := token if {
	header := http_request.headers.authorization
	startswith(header, "Bearer ")
	token := substring(header, count("Bearer "), -1)
}

subject := demo_directory[bearer_token]

# ---------------------------------------------------------------------------
# 2. The open set: requests that may proceed unauthenticated.
#
# Enumerated as data rather than as rules so the whole set is readable at a
# glance, which is what somebody reviewing this policy needs to check first.
# ---------------------------------------------------------------------------

public_routes := {
	{"method": "GET", "path": "/health"},
	{"method": "GET", "path": "/ready"},
}

allow if {
	{"method": method, "path": path} in public_routes
}

# ---------------------------------------------------------------------------
# 3. The authenticated rules. Each one names its role.
# ---------------------------------------------------------------------------

# A reader may list the document index.
allow if {
	method == "GET"
	path == "/api/documents"
	"reader" in subject.roles
}

# A reader may fetch one document.
allow if {
	method == "GET"
	document_path
	"reader" in subject.roles
}

# Only an owner may delete. A reader token on this route is denied, which is the
# separation an agency is usually asked to evidence: read access and destructive
# access are different grants, not the same login.
allow if {
	method == "DELETE"
	document_path
	"owner" in subject.roles
}

# document_path matches /api/documents/{id} and only that. It is deliberately
# not a prefix match: /api/documents/doc-1001/audit-trail has four segments and
# is therefore outside the closed set, so it is denied rather than inherited.
document_path if {
	segments := split(trim_prefix(path, "/"), "/")
	count(segments) == 3
	segments[0] == "api"
	segments[1] == "documents"
	segments[2] != ""
}

# ---------------------------------------------------------------------------
# 4. The decision Envoy receives.
#
# `result` is what the ext_authz plugin reads. On allow it carries the identity
# headers Envoy stamps onto the request before forwarding it upstream. Envoy
# SETS these headers rather than appending, so a client that sent its own
# x-authz-subject has that value overwritten by the verified one here. The
# upstream service therefore reads a header it can trust without doing any
# verification itself.
#
# On deny it carries an explicit 403 and a small body. Nothing about the reason
# for the denial is disclosed: which rule failed is in the decision log, not in
# the response to a caller who is probing.
# ---------------------------------------------------------------------------

default result := {
	"allowed": false,
	"http_status": 403,
	"headers": {"x-authz-decision": "deny"},
	"body": "{\"error\":\"forbidden\"}",
}

result := {
	"allowed": true,
	"headers": stamped_identity,
} if {
	allow
}

# An authenticated caller is stamped with the verified subject and roles.
stamped_identity := {
	"x-authz-subject": subject.id,
	"x-authz-roles": concat(",", sort(subject.roles)),
	"x-authz-decision": "allow",
} if {
	subject
}

# An unauthenticated caller on a public route is stamped too, with a subject
# that grants nothing. Stamping unconditionally is what guarantees the upstream
# never sees a leftover client-supplied value in these header names.
stamped_identity := {
	"x-authz-subject": "anonymous",
	"x-authz-roles": "none",
	"x-authz-decision": "allow-public",
} if {
	not subject
}
