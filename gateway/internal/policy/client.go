// Package policy provides a client for the Open Policy Agent (OPA) REST API.
// All policy checks degrade gracefully (allow) when OPA is unreachable or unconfigured.
package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	pathRetrieval = "/v1/data/raggateway/retrieval/allow"
	pathCompile   = "/v1/data/raggateway/adapter/allow"
	pathOutput    = "/v1/data/raggateway/output/allow"
)

// Client communicates with OPA for policy decisions.
type Client struct {
	endpoint string
	http     *http.Client
}

// NewClient returns a policy Client targeting the given OPA endpoint.
// If endpoint is empty, all checks degrade gracefully to allow=true.
func NewClient(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		http: &http.Client{
			Timeout: 2 * time.Second, // policy checks must be fast
		},
	}
}

// opaInput is the request body sent to OPA's /v1/data endpoint.
type opaInput struct {
	Input map[string]any `json:"input"`
}

// opaResult is the minimal response shape from OPA's /v1/data endpoint.
type opaResult struct {
	Result bool `json:"result"`
}

// CheckRetrieval evaluates whether a retrieval request is allowed.
// Input: user_role and doc_trust_tiers ([]string).
// Returns allow=true on OPA errors (graceful degrade).
func (c *Client) CheckRetrieval(ctx context.Context, userRole string, docTrustTiers []string) (bool, error) {
	if c.endpoint == "" {
		return true, nil
	}
	return c.query(ctx, pathRetrieval, map[string]any{
		"user_role":       userRole,
		"doc_trust_tiers": docTrustTiers,
	})
}

// CheckCompile evaluates whether a compile-to-LoRA request is allowed.
// Input: user_role and section_ids ([]string).
// Returns allow=true on OPA errors (graceful degrade).
func (c *Client) CheckCompile(ctx context.Context, userRole string, sectionIDs []string) (bool, error) {
	if c.endpoint == "" {
		return true, nil
	}
	return c.query(ctx, pathCompile, map[string]any{
		"user_role":   userRole,
		"section_ids": sectionIDs,
	})
}

// CheckOutput evaluates whether the LLM's response may be forwarded to the caller.
// Input: user_role and response_has_citation (bool).
// Returns allow=true on OPA errors (graceful degrade).
func (c *Client) CheckOutput(ctx context.Context, userRole string, responseHasCitation bool) (bool, error) {
	if c.endpoint == "" {
		return true, nil
	}
	return c.query(ctx, pathOutput, map[string]any{
		"user_role":             userRole,
		"response_has_citation": responseHasCitation,
	})
}

// query posts an OPA input document to path and returns the boolean result.
// Errors (network, decode) return allow=true to degrade gracefully.
func (c *Client) query(ctx context.Context, path string, input map[string]any) (bool, error) {
	body, err := json.Marshal(opaInput{Input: input})
	if err != nil {
		return true, fmt.Errorf("policy: marshal input: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return true, fmt.Errorf("policy: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// OPA unreachable — degrade gracefully.
		return true, fmt.Errorf("policy: OPA unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// Policy rule not defined — default allow (no policy = no restriction).
		return true, nil
	}
	if resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("policy: OPA returned %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return true, fmt.Errorf("policy: read response: %w", err)
	}

	var result opaResult
	if err := json.Unmarshal(data, &result); err != nil {
		return true, fmt.Errorf("policy: decode response: %w", err)
	}
	return result.Result, nil
}
