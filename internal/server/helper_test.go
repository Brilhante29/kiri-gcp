package server_test

import (
	"testing"

	kiri "github.com/Brilhante29/kiri"
)

// kiriNewServer starts a fresh in-process emulator for testing.
// The server includes both HTTP (REST) and gRPC listeners on random ports.
func kiriNewServer(t *testing.T) *kiri.Server {
	t.Helper()
	return kiri.NewServer()
}

// kiriNewServerWithGRPC starts a server and returns its gRPC address.
func kiriNewServerWithGRPC(t *testing.T) (*kiri.Server, string) {
	t.Helper()
	srv := kiri.NewServer()
	return srv, srv.GRPCURL
}
