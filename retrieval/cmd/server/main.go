package main

import (
	"log"
	"net"
	"os"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	grpcserver "github.com/b11902156/rag-gateway/retrieval/internal/grpc"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("failed to init logger: %v", err)
	}
	defer logger.Sync()

	port := os.Getenv("RETRIEVAL_GRPC_PORT")
	if port == "" {
		port = "50051"
	}

	lis, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	s := grpc.NewServer()
	grpcserver.Register(s, logger)

	logger.Info("starting retrieval service", zap.String("port", port))
	if err := s.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}
