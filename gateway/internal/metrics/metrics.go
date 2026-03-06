// Package metrics defines Prometheus counters and histograms for the RAG Gateway.
//
// Security-relevant counters tracked here:
//   - rag_firewall_sections_blocked_total   – sections dropped by context firewall
//   - rag_firewall_sentences_stripped_total – sentences stripped from sections
//   - rag_policy_denied_total               – requests denied by OPA (labelled by reason)
//   - rag_cite_or_refuse_total              – cite-or-refuse rejections
//   - adapter_probe_failures_total          – canary probe failures (labelled by probe_name)
//   - http_requests_total                   – HTTP request counters (path, status_class)
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// FirewallSectionsBlocked counts sections dropped entirely by the context
	// firewall — either due to trust-tier mismatch or fully-hostile content.
	FirewallSectionsBlocked = promauto.NewCounter(prometheus.CounterOpts{
		Name: "rag_firewall_sections_blocked_total",
		Help: "Total sections blocked by the context firewall (trust-tier or full injection).",
	})

	// FirewallSentencesStripped counts individual sentences removed from sections
	// because they contained injection patterns.
	FirewallSentencesStripped = promauto.NewCounter(prometheus.CounterOpts{
		Name: "rag_firewall_sentences_stripped_total",
		Help: "Total sentences stripped from retrieved sections by the injection filter.",
	})

	// PolicyDenied counts requests denied by the OPA policy engine.
	// The "reason" label distinguishes retrieval denials from compile denials.
	PolicyDenied = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "rag_policy_denied_total",
		Help: "Total requests denied by the OPA policy engine.",
	}, []string{"reason"})

	// CiteOrRefuse counts requests rejected under the cite-or-refuse rule —
	// i.e. the retrieval returned sections but the firewall stripped them all.
	CiteOrRefuse = promauto.NewCounter(prometheus.CounterOpts{
		Name: "rag_cite_or_refuse_total",
		Help: "Total requests rejected by cite-or-refuse (no valid sections after firewall).",
	})

	// AdapterProbeFails counts per-probe canary failures after a LoRA compile.
	// The "probe_name" label identifies which probe failed.
	AdapterProbeFails = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "adapter_probe_failures_total",
		Help: "Total adapter canary probe failures.",
	}, []string{"probe_name"})

	// RequestsTotal counts HTTP requests handled by the gateway.
	// "path" is the route path; "status_class" is "2xx", "4xx", "5xx", etc.
	RequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total HTTP requests handled by the gateway.",
	}, []string{"path", "status_class"})
)
