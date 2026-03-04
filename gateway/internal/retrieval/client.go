// Package retrieval provides a gRPC client for the retrieval orchestrator.
package retrieval

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/b11902156/rag-gateway/gateway/internal/pb/retrieval/v1"
)

// Section is a retrieved document section returned to callers.
type Section struct {
	DocumentID string
	SectionID  string
	Content    string
	Score      float32
	TrustTier  string
	Metadata   map[string]string
}

// Client wraps the gRPC retrieval service connection.
type Client struct {
	conn   *grpc.ClientConn
	client pb.RetrievalServiceClient
	logger *zap.Logger
}

// New dials the retrieval orchestrator at addr (e.g. "localhost:50051").
func New(addr string, logger *zap.Logger) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("retrieval: dial %s: %w", addr, err)
	}
	return &Client{
		conn:   conn,
		client: pb.NewRetrievalServiceClient(conn),
		logger: logger,
	}, nil
}

// Close releases the underlying connection.
func (c *Client) Close() {
	if c.conn != nil {
		c.conn.Close()
	}
}

// Retrieve fetches the top-k sections for query from the retrieval service.
func (c *Client) Retrieve(ctx context.Context, query, traceID string, topK int32) ([]Section, error) {
	resp, err := c.client.Retrieve(ctx, &pb.RetrieveRequest{
		Query:   query,
		TraceId: traceID,
		TopK:    topK,
	})
	if err != nil {
		return nil, fmt.Errorf("retrieval: Retrieve RPC: %w", err)
	}
	sections := make([]Section, 0, len(resp.Sections))
	for _, s := range resp.Sections {
		sections = append(sections, Section{
			DocumentID: s.DocumentId,
			SectionID:  s.SectionId,
			Content:    s.Content,
			Score:      s.Score,
			TrustTier:  s.TrustTier,
			Metadata:   s.Metadata,
		})
	}
	return sections, nil
}

// Index submits a document for indexing.
func (c *Client) Index(ctx context.Context, documentID, content string, metadata map[string]string) error {
	resp, err := c.client.Index(ctx, &pb.IndexRequest{
		DocumentId: documentID,
		Content:    content,
		Metadata:   metadata,
	})
	if err != nil {
		return fmt.Errorf("retrieval: Index RPC: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("retrieval: index failed: %s", resp.Message)
	}
	c.logger.Info("retrieval: indexed document", zap.String("document_id", documentID), zap.String("msg", resp.Message))
	return nil
}
