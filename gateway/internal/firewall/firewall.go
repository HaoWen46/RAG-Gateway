package firewall

import "strings"

// ContextFirewall sanitizes retrieved content before it reaches the LLM.
type ContextFirewall struct{}

func New() *ContextFirewall {
	return &ContextFirewall{}
}

// injectionPatterns are instruction-like patterns to strip from retrieved text.
var injectionPatterns = []string{
	"ignore previous instructions",
	"ignore all previous",
	"you are now",
	"system prompt",
	"override",
}

// Sanitize strips instruction-like content and override patterns from text.
func (f *ContextFirewall) Sanitize(text string) string {
	lower := strings.ToLower(text)
	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, pattern) {
			text = "" // Block entirely if injection detected
			break
		}
	}
	return text
}
