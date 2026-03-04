// Package grpc implements the RetrievalService gRPC server.
// It delegates actual indexing and retrieval to the pageindex-worker service,
// which it reaches via a downstream gRPC connection.
package grpc

import (
	"context"
	"os"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/b11902156/rag-gateway/retrieval/internal/pb/retrieval/v1"
)

// server implements pb.RetrievalServiceServer by forwarding calls to
// the pageindex-worker.
type server struct {
	pb.UnimplementedRetrievalServiceServer
	downstream pb.RetrievalServiceClient
	logger     *zap.Logger
}

// Register wires the RetrievalService into s and dials the pageindex-worker.
func Register(s *grpc.Server, logger *zap.Logger) {
	addr := os.Getenv("PAGEINDEX_GRPC_ADDR")
	if addr == "" {
		addr = "localhost:50052"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		// Non-fatal: server starts but every RPC will fail until worker is up.
		logger.Warn("retrieval: could not dial pageindex-worker", zap.String("addr", addr), zap.Error(err))
		pb.RegisterRetrievalServiceServer(s, &server{logger: logger})
		return
	}

	downstream := pb.NewRetrievalServiceClient(conn)
	pb.RegisterRetrievalServiceServer(s, &server{downstream: downstream, logger: logger})
	logger.Info("retrieval gRPC service registered", zap.String("pageindex_addr", addr))
}

func (srv *server) Retrieve(ctx context.Context, req *pb.RetrieveRequest) (*pb.RetrieveResponse, error) {
	if srv.downstream == nil {
		return &pb.RetrieveResponse{}, nil
	}
	srv.logger.Info("retrieve",
		zap.String("trace_id", req.TraceId),
		zap.String("query", req.Query),
		zap.Int32("top_k", req.TopK),
	)
	return srv.downstream.Retrieve(ctx, req)
}

func (srv *server) Index(ctx context.Context, req *pb.IndexRequest) (*pb.IndexResponse, error) {
	if srv.downstream == nil {
		return &pb.IndexResponse{Success: false, Message: "pageindex-worker unavailable"}, nil
	}
	srv.logger.Info("index", zap.String("document_id", req.DocumentId))
	return srv.downstream.Index(ctx, req)
}
