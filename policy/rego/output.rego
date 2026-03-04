package raggateway.output

default action := "allow"

# Require citations for all responses.
action := "cite_required" if {
    input.has_retrieval_context
}

# Refuse if no supporting documents found.
action := "refuse" if {
    input.has_retrieval_context
    count(input.retrieved_sections) == 0
}

# Redact if output contains sensitive patterns.
action := "redact" if {
    input.sensitivity_score > 0.8
}
