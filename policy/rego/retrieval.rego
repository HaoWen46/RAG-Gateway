package raggateway.retrieval

default allow := false

# Allow retrieval if user role has access to ALL document trust tiers in the result set.
allow if {
    input.user_role == "admin"
}

allow if {
    input.user_role == "analyst"
    every tier in input.doc_trust_tiers {
        tier in {"public", "internal"}
    }
}

allow if {
    input.user_role == "viewer"
    every tier in input.doc_trust_tiers {
        tier == "public"
    }
}
