// Package main is the entry point for the kiri emulator binary.
package main

import (
	"flag"
	"fmt"
	"os"

	_ "github.com/kiri-dev/kiri/internal/registry" // Register all services via init().
	"github.com/kiri-dev/kiri/internal/server"
)

func main() {
	cfg := server.DefaultConfig()

	host := flag.String("host", cfg.Host, "Server bind address (overrides KIRI_HOST)")
	httpPort := flag.Int("http-port", cfg.HTTPPort, "HTTP REST port (overrides KIRI_HTTP_PORT)")
	grpcPort := flag.Int("grpc-port", cfg.GRPCPort, "gRPC port (overrides KIRI_GRPC_PORT); 0 disables gRPC")
	fidelity := flag.String("fidelity", "", "Comma-separated list of fidelities to enable (e.g. A,B)")
	state := flag.String("state", "", "Comma-separated list of states to enable (e.g. behavioral,integrated)")
	flag.Parse()

	cfg.Host = *host
	cfg.HTTPPort = *httpPort
	cfg.GRPCPort = *grpcPort
	cfg.Fidelity = *fidelity
	cfg.State = *state

	srv := server.New(cfg)

	if err := srv.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "kiri:", err)
		os.Exit(1)
	}
}
