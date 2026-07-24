// Package kiri provides a public API for running an in-process GCP service
// emulator.
//
// Usage:
//
//	srv := kiri.NewServer()
//	defer srv.Close()
//
//	client, _ := storage.NewClient(ctx,
//	    option.WithEndpoint(srv.URL),
//	    option.WithoutAuthentication(),
//	)
//
// For gRPC clients (Pub/Sub, Firestore):
//
//	conn, _ := grpc.NewClient(srv.GRPCURL, grpc.WithTransportCredentials(insecure.NewCredentials()))
//	pubsubClient := pubsubpb.NewPublisherClient(conn)
package kiri

import (
	"net/http/httptest"

	// Register all services via init(). See internal/registry for the single
	// canonical list shared with the CLI and the README generator.
	_ "github.com/Brilhante29/kiri-gcp/internal/registry"
	"github.com/Brilhante29/kiri-gcp/internal/server"
)

// Server is an in-process GCP emulator wrapping an HTTP + gRPC server.
type Server struct {
	// URL is the base REST URL, e.g. "http://127.0.0.1:PORT".
	URL string

	// GRPCURL is the base gRPC URL, e.g. "127.0.0.1:PORT".
	GRPCURL string

	httpServer *httptest.Server
	internal   *server.Server
}

// NewServer creates and starts an in-process emulator on random localhost
// ports for both HTTP and gRPC. Use srv.URL for REST clients and srv.GRPCURL
// for gRPC clients.
func NewServer() *Server {
	cfg := server.DefaultConfig()
	cfg.LogLevel = 100 // Suppress logs in test mode.
	cfg.HTTPPort = 0   // Random port.
	cfg.GRPCPort = 0
	internal := server.New(cfg)

	ts := httptest.NewServer(internal.Handler())

	// Start gRPC and wait for it to bind the random port.
	grpcReady := make(chan struct{})
	go func() {
		_ = internal.GRPCStart(grpcReady)
	}()

	<-grpcReady

	return &Server{
		URL:        ts.URL,
		GRPCURL:    internal.GRPCAddr(),
		httpServer: ts,
		internal:   internal,
	}
}

// Close shuts down the server.
func (s *Server) Close() {
	if s.httpServer != nil {
		s.httpServer.Close()
	}

	s.internal.GRPCStop()
}
