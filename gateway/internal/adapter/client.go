// Package adapter provides a gRPC client for the Adapter Service.
package adapter

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/b11902156/rag-gateway/gateway/internal/pb/adapter/v1"
)

// Result holds the outcome of a Compile RPC.
type Result struct {
	AdapterID string
	Signature string
	ExpiresAt int64 // Unix timestamp
}

// ProbeResult holds the outcome of a single canary probe.
type ProbeResult struct {
	ProbeName string
	Passed    bool
	Detail    string
}

// Client wraps the gRPC Adapter Service connection.
type Client struct {
	conn   *grpc.ClientConn
	client pb.AdapterServiceClient
	logger *zap.Logger
}

// New dials the Adapter Service at addr (e.g. "localhost:50053").
func New(addr string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("adapter: dial %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: pb.NewAdapterServiceClient(conn),
		logger: logger,
	}, nil
}

// Close releases the underlying gRPC connection.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Compile requests adapter generation from the Adapter Service.
// Returns the adapter ID, HMAC signature, and expiry timestamp on success.
func (c *Client) Compile(ctx context.Context, sessionID, traceID string, sectionIDs []string, ttlSeconds int32) (*Result, error) {
	resp, err := c.client.Compile(ctx, &pb.CompileRequest{
		SessionId:  sessionID,
		TraceId:    traceID,
		SectionIds: sectionIDs,
		TtlSeconds: ttlSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("adapter: Compile RPC: %w", err)
	}
	c.logger.Info("adapter: compile complete",
		zap.String("adapter_id", resp.AdapterId),
		zap.String("trace_id", traceID),
	)
	return &Result{
		AdapterID: resp.AdapterId,
		Signature: resp.Signature,
		ExpiresAt: resp.ExpiresAt,
	}, nil
}

// Verify checks adapter integrity and runs canary probes.
// Returns (valid, probe results, error). If valid is false, the adapter must be revoked.
func (c *Client) Verify(ctx context.Context, adapterID, signature string) (bool, []ProbeResult, error) {
	resp, err := c.client.Verify(ctx, &pb.VerifyRequest{
		AdapterId: adapterID,
		Signature: signature,
	})
	if err != nil {
		return false, nil, fmt.Errorf("adapter: Verify RPC: %w", err)
	}
	probes := make([]ProbeResult, len(resp.ProbeResults))
	for i, p := range resp.ProbeResults {
		probes[i] = ProbeResult{
			ProbeName: p.ProbeName,
			Passed:    p.Passed,
			Detail:    p.Detail,
		}
	}
	return resp.Valid, probes, nil
}

// Revoke invalidates an adapter in the Adapter Service.
func (c *Client) Revoke(ctx context.Context, adapterID string) error {
	resp, err := c.client.Revoke(ctx, &pb.RevokeRequest{AdapterId: adapterID})
	if err != nil {
		return fmt.Errorf("adapter: Revoke RPC: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("adapter: revoke failed for %s", adapterID)
	}
	return nil
}
