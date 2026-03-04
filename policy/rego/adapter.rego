package raggateway.adapter

default allow_compile := false

# Only admin and analyst roles can trigger compile-to-LoRA.
allow_compile if {
    input.user_role in {"admin", "analyst"}
}

# TTL must be between 5 and 30 minutes.
valid_ttl if {
    input.ttl_seconds >= 300
    input.ttl_seconds <= 1800
}

# Compile is allowed when role check and TTL are valid.
allow if {
    allow_compile
    valid_ttl
}
