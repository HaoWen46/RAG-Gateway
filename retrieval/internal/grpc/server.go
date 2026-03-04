package grpc

import (
	"context"

	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// RetrievalServer implements the retrieval gRPC service.
type RetrievalServer struct {
	logger *zap.Logger
}

// Register registers the retrieval gRPC service.
// TODO: Register with generated protobuf service descriptor once protos are compiled.
func Register(s *grpc.Server, logger *zap.Logger) {
	_ = &RetrievalServer{logger: logger}
	// TODO: pb.RegisterRetrievalServiceServer(s, srv)
	logger.Info("retrieval gRPC service registered (stub)")
}

// Retrieve handles retrieval requests.
func (s *RetrievalServer) Retrieve(ctx context.Context, query string, topK int) ([]Section, error) {
	s.logger.Info("retrieve called (stub)", zap.String("query", query))
	return nil, nil
}

// Section represents a retrieved document section.
type Section struct {
	DocumentID string
	SectionID  string
	Content    string
	Score      float64
	TrustTier  string
}
