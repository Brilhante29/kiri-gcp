package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"time"

	"github.com/Brilhante29/kiri-gcp/internal/grpcutil"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

// GRPCConfig holds gRPC-specific server settings.
type GRPCConfig struct {
	Host string
	Port int
}

// GRPCServer wraps a gRPC server with lifecycle helpers.
type GRPCServer struct {
	config   GRPCConfig
	server   *grpc.Server
	logger   *slog.Logger
	listener net.Listener
}

// newGRPCServer creates a gRPC server and registers all gRPC service
// implementations passed via register. Each register function receives
// the *grpc.Server to call Register*Service on.
func newGRPCServer(cfg GRPCConfig, logger *slog.Logger, registers ...func(*grpc.Server)) *GRPCServer {
	// ForceServerCodec bypasses content-subtype-based codec negotiation
	// entirely. It is required, not cosmetic: grpcutil's raw-bytes codec
	// registers under the name "proto" — the same name grpc-go's own
	// built-in codec (google.golang.org/grpc/encoding/proto) uses — and
	// package init() order between the two is not guaranteed by Go, so
	// relying on name-based negotiation let the standard codec win the
	// registration race in practice. That codec expects every message to
	// implement proto.Message and rejects our raw []byte with "message is
	// *[]uint8, want proto.Message" — confirmed against a real client
	// (cloud.google.com/go/pubsub), not a hypothetical. Forcing the codec
	// makes this server always use grpcutil's codec regardless of what the
	// client negotiates, which is safe here because the wire bytes are
	// still valid protobuf either way — only the Go-side (un)marshaling
	// entry point differs.
	gs := grpc.NewServer(
		grpc.UnaryInterceptor(unaryLogger(logger)),
		grpc.ForceServerCodec(grpcutil.Codec),
	)

	for _, reg := range registers {
		reg(gs)
	}

	// Enable reflection so grpcurl and other tools can discover services.
	reflection.Register(gs)

	return &GRPCServer{
		config: cfg,
		server: gs,
		logger: logger,
	}
}

// Start begins listening and serving gRPC. It returns once the listener is
// open (readyCh closed) or an error occurs. On listen error, readyCh is
// closed before returning so callers waiting on it do not hang.
func (g *GRPCServer) Start(readyCh ...chan struct{}) error {
	addr := fmt.Sprintf("%s:%d", g.config.Host, g.config.Port)
	g.logger.Info("starting gRPC server", "addr", addr)

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", addr)
	if err != nil {
		// Close readyCh so callers waiting on it don't hang forever when
		// the port is already in use or bind fails for any other reason.
		// The error is returned to the caller, which is the real signal.
		if len(readyCh) > 0 && readyCh[0] != nil {
			close(readyCh[0])
		}
		return fmt.Errorf("grpc listen on %s: %w", addr, err)
	}

	g.listener = ln

	if len(readyCh) > 0 && readyCh[0] != nil {
		close(readyCh[0])
	}

	return g.server.Serve(ln)
}

// Addr returns the bound address (host:port). Available after Start.
func (g *GRPCServer) Addr() string {
	if g.listener != nil {
		return g.listener.Addr().String()
	}
	return fmt.Sprintf("%s:%d", g.config.Host, g.config.Port)
}

// GracefulStop stops the gRPC server gracefully.
func (g *GRPCServer) GracefulStop() {
	if g.server != nil {
		g.logger.Info("shutting down gRPC server")
		g.server.GracefulStop()
	}
}

// unaryLogger is a gRPC unary interceptor that logs each RPC call.
func unaryLogger(logger *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		logger.Info("grpc request",
			"method", info.FullMethod,
			"duration", time.Since(start).String(),
		)
		return resp, err
	}
}
