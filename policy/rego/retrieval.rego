package raggateway.retrieval

default allow := false

# Allow retrieval if user role has access to the document's trust tier.
allow if {
    input.user_role == "admin"
}

allow if {
    input.user_role == "analyst"
    input.doc_trust_tier in {"public", "internal"}
}

allow if {
    input.user_role == "viewer"
    input.doc_trust_tier == "public"
}
