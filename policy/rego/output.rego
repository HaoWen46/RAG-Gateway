package raggateway.output

default allow := true

# Deny if RAG context was injected but the response contains no citation.
allow := false if {
    input.has_retrieval_context
    not input.response_has_citation
}
