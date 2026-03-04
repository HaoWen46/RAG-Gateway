// Package firewall sanitizes retrieved content before it reaches the LLM.
// It performs sentence-level injection stripping and trust-tier enforcement
// to mitigate prompt-injection attacks embedded in retrieved documents.
package firewall

import (
	"regexp"
	"strings"

	"github.com/b11902156/rag-gateway/gateway/internal/retrieval"
)

// trustTierRank maps trust tier names to an ordinal (higher = more sensitive).
var trustTierRank = map[string]int{
	"public":       0,
	"internal":     1,
	"confidential": 2,
	"secret":       3,
}

// roleMaxTier maps JWT roles to the highest trust tier they may access.
var roleMaxTier = map[string]int{
	"admin":    3,
	"analyst":  1,
	"viewer":   0,
	"":         0, // anonymous / unknown
}

// injectionREs are compiled regexes for instruction-injection patterns.
// Each matches a sentence or fragment that attempts to hijack the LLM.
var injectionREs = []*regexp.Regexp{
	regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions?`),
	regexp.MustCompile(`(?i)ignore\s+the\s+above`),
	regexp.MustCompile(`(?i)disregard\s+(all\s+)?(previous|prior|above)`),
	regexp.MustCompile(`(?i)you\s+are\s+now\s+a`),
	regexp.MustCompile(`(?i)act\s+as\s+(an?\s+)?(unrestricted|jailbroken|evil|DAN)`),
	regexp.MustCompile(`(?i)system\s+prompt\s*:`),
	regexp.MustCompile(`(?i)<\s*system\s*>`),
	regexp.MustCompile(`(?i)\[INST\]`),
	regexp.MustCompile(`(?i)override\s+(all\s+)?(safety|guidelines|rules|instructions)`),
	regexp.MustCompile(`(?i)forget\s+(all\s+)?previous\s+(instructions?|context)`),
	regexp.MustCompile(`(?i)new\s+instructions?\s*:`),
	regexp.MustCompile(`(?i)do\s+not\s+follow\s+(previous|prior|the\s+above)`),
}

// ContextFirewall sanitizes retrieved content before it reaches the LLM.
type ContextFirewall struct{}

// New returns a new ContextFirewall.
func New() *ContextFirewall { return &ContextFirewall{} }

// SanitizeSections filters and sanitizes a slice of retrieved sections:
//  1. Drops sections whose trust tier exceeds the caller's role maximum.
//  2. Strips sentences containing injection patterns from each section's content.
//  3. Drops sections whose content becomes empty after stripping.
//
// userRole is the JWT role claim (e.g. "admin", "analyst", "viewer").
func (f *ContextFirewall) SanitizeSections(sections []retrieval.Section, userRole string) []retrieval.Section {
	maxTier := roleMaxTier[userRole] // defaults to 0 (public) for unknown roles
	out := make([]retrieval.Section, 0, len(sections))
	for _, s := range sections {
		// Trust-tier gate.
		tier, ok := trustTierRank[strings.ToLower(s.TrustTier)]
		if !ok {
			tier = 0 // unknown tiers treated as public
		}
		if tier > maxTier {
			continue // user not authorised for this tier
		}

		// Sentence-level injection stripping.
		clean := stripInjections(s.Content)
		if strings.TrimSpace(clean) == "" {
			continue // entire content was hostile; drop section
		}
		s.Content = clean
		out = append(out, s)
	}
	return out
}

// Sanitize sanitizes a single text string (sentence-level stripping).
// Used for ad-hoc text fragments outside of the RAG pipeline.
func (f *ContextFirewall) Sanitize(text string) string {
	return stripInjections(text)
}

// stripInjections removes sentences that contain injection patterns.
// Sentences are split on ". ", "! ", "? ", "\n" boundaries.
func stripInjections(text string) string {
	sentences := splitSentences(text)
	kept := sentences[:0]
	for _, s := range sentences {
		if !hasInjection(s) {
			kept = append(kept, s)
		}
	}
	return strings.Join(kept, " ")
}

// hasInjection returns true if text matches any injection pattern.
func hasInjection(text string) bool {
	for _, re := range injectionREs {
		if re.MatchString(text) {
			return true
		}
	}
	return false
}

// splitSentences splits text into sentence-like fragments.
func splitSentences(text string) []string {
	// Split on line breaks first, then on sentence-ending punctuation.
	lines := strings.Split(text, "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Further split on '. ', '! ', '? '
		frags := regexp.MustCompile(`(?:[.!?])\s+`).Split(line, -1)
		for _, f := range frags {
			f = strings.TrimSpace(f)
			if f != "" {
				out = append(out, f)
			}
		}
	}
	if len(out) == 0 {
		return []string{text}
	}
	return out
}
