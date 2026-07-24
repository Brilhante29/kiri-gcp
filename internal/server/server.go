// Package server wires the emulator together: it builds the router, registers
// every service from the global registry, serves HTTP, and shuts down
// gracefully, saving persistent snapshots for services that support it.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	firestoregrpc "github.com/Brilhante29/kiri-gcp/internal/grpcsvc/firestore"
	pubsubgrpc "github.com/Brilhante29/kiri-gcp/internal/grpcsvc/pubsub"

	"github.com/Brilhante29/kiri-gcp/internal/service"
	cloudschedulerpkg "github.com/Brilhante29/kiri-gcp/internal/service/cloudscheduler"
	gcspkg "github.com/Brilhante29/kiri-gcp/internal/service/gcs"
	pubsubpkg "github.com/Brilhante29/kiri-gcp/internal/service/pubsub"
)

// Config holds the server configuration.
type Config struct {
	Host     string
	HTTPPort int
	GRPCPort int
	LogLevel slog.Level
	Fidelity string
	State    string
}

// DefaultConfig returns configuration derived from KIRI_* environment
// variables, falling back to sensible defaults.
func DefaultConfig() Config {
	cfg := Config{
		Host:     "0.0.0.0",
		HTTPPort: 4443,
		GRPCPort: 8085,
		LogLevel: parseLogLevel(os.Getenv("KIRI_LOG_LEVEL"), slog.LevelInfo),
	}

	if host := os.Getenv("KIRI_HOST"); host != "" {
		cfg.Host = host
	}

	if p := os.Getenv("KIRI_HTTP_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			cfg.HTTPPort = n
		}
	}

	if p := os.Getenv("KIRI_GRPC_PORT"); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			cfg.GRPCPort = n
		}
	}

	return cfg
}

func parseLogLevel(s string, def slog.Level) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return def
	}
}

// Server is the kiri HTTP server.
type Server struct {
	config   Config
	router   *Router
	registry *service.Registry
	logger   *slog.Logger
	http     *http.Server
	grpc     *GRPCServer
}

// New creates a server and registers every service from the global registry.
func New(config Config) *Server {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: config.LogLevel}))
	registry := service.NewRegistry()
	router := NewRouter()

	srv := &Server{
		config:   config,
		router:   router,
		registry: registry,
		logger:   logger,
	}

	// Register HTTP services from the global registry.
	for _, svc := range service.Services() {
		if !matchFidelity(svc, config.Fidelity) {
			continue
		}
		if !matchState(svc, config.State) {
			continue
		}
		registry.Register(svc)
		svc.RegisterRoutes(router)
		logger.Debug("registered HTTP service", "name", svc.Name())
	}

	// Root health/discovery handler.
	router.Handle("GET", "/", srv.handleRoot)

	// Build gRPC server with service implementations. Pub/Sub REST and gRPC
	// share one backend: internal/service/pubsub owns it and hands it here,
	// so a PUBSUB_EMULATOR_HOST (gRPC) client and a REST client see the
	// exact same topics, subscriptions, and messages. Looked up from the
	// global (unfiltered) registry since the gRPC server is always built
	// regardless of any --fidelity/--state HTTP filter.
	var pubsubBackend *pubsubgrpc.MergeBackend

	for _, svc := range service.Services() {
		if ps, ok := svc.(*pubsubpkg.Service); ok {
			pubsubBackend = ps.Backend()

			break
		}
	}

	if pubsubBackend == nil {
		pubsubBackend = pubsubgrpc.NewMergeBackend()
	}

	firestoreBackend := firestoregrpc.NewMergeBackend()
	gcfg := GRPCConfig{Host: config.Host, Port: config.GRPCPort}

	grpcSrv := newGRPCServer(gcfg, logger,
		pubsubgrpc.RegisterWith(pubsubBackend),
		firestoregrpc.RegisterWith(firestoreBackend),
	)
	srv.grpc = grpcSrv

	// Wire cross-service hooks: GCS notifications + Cloud Scheduler -> Pub/Sub.
	for _, svc := range registry.All() {
		if ps, ok := svc.(*pubsubpkg.Service); ok {
			publishOne := func(topicPath, data string, attrs map[string]string) []string {
				return ps.Publish(topicPath, []pubsubpkg.Message{{
					Data:       data,
					Attributes: attrs,
				}})
			}
			gcspkg.PublishFunc = publishOne
			cloudschedulerpkg.PublishFunc = publishOne
			logger.Info("cross-service wiring: GCS + Cloud Scheduler -> Pub/Sub")
		}
	}

	return srv
}

func matchFidelity(svc service.Service, filter string) bool {
	if filter == "" {
		return true
	}
	desc, ok := svc.(service.Describer)
	if !ok {
		return true
	}
	allowed := strings.Split(filter, ",")
	meta := desc.Meta()
	for _, a := range allowed {
		if strings.EqualFold(meta.Fidelity.Label(), strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

func matchState(svc service.Service, filter string) bool {
	if filter == "" {
		return true
	}
	desc, ok := svc.(service.Describer)
	if !ok {
		return true
	}
	allowed := strings.Split(filter, ",")
	meta := desc.Meta()
	for _, a := range allowed {
		if strings.EqualFold(meta.State.Label(), strings.TrimSpace(a)) {
			return true
		}
	}
	return false
}

// handleRoot answers GET / with a small identifying JSON document so that a
// bare curl against the emulator confirms it is alive and lists services.
func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	names := s.registry.Names()
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_, _ = fmt.Fprintf(w, `{"emulator":"kiri","status":"ok","services":%d,"grpc_port":%d}`, len(names), s.config.GRPCPort)
}

// Handler returns the composed HTTP handler (logging + router) for in-process
// testing via httptest.NewServer.
func (s *Server) Handler() http.Handler {
	return logging(s.logger, s.router)
}

// Addr returns the configured bind address.
func (s *Server) Addr() string {
	return fmt.Sprintf("%s:%d", s.config.Host, s.config.HTTPPort)
}

// Registry exposes the service registry.
func (s *Server) Registry() *service.Registry { return s.registry }

// GRPCAddr returns the gRPC server address (host:port). If the gRPC server
// is already listening, returns the actual bound address (important when
// using random port 0). Falls back to the configured address if not started.
func (s *Server) GRPCAddr() string {
	if s.grpc == nil {
		return ""
	}
	return s.grpc.Addr()
}

// GRPCStart starts the gRPC server in the background. If readyCh is provided,
// it is closed once the listener is active. Used by the in-process test helper.
func (s *Server) GRPCStart(readyCh ...chan struct{}) error {
	return s.grpc.Start(readyCh...)
}

// GRPCStop gracefully stops the gRPC server.
func (s *Server) GRPCStop() {
	if s.grpc != nil {
		s.grpc.GracefulStop()
	}
}

// Start begins serving HTTP and gRPC and blocks until the server stops.
// It returns an error if either listener fails to bind before becoming ready.
func (s *Server) Start(readyCh ...chan struct{}) error {
	if s.config.GRPCPort > 0 {
		s.logger.Info("gRPC service available", "addr", s.GRPCAddr(), "env_hint", "PUBSUB_EMULATOR_HOST="+s.GRPCAddr()+", FIRESTORE_EMULATOR_HOST="+s.GRPCAddr())
	}

	// httpReady and grpcReady are closed by their respective goroutines once
	// the listener is up (or failed to bind). startErr carries the first
	// startup error back to the caller; it is buffered so the goroutines
	// never block after Start returns.
	httpReady := make(chan struct{}, 1)
	grpcReady := make(chan struct{}, 1)
	startErr := make(chan error, 2)

	go func() {
		s.http = &http.Server{
			Handler:           s.Handler(),
			ReadHeaderTimeout: 10 * time.Second,
		}

		for _, name := range s.registry.Names() {
			s.logger.Info("REST service available", "name", name)
		}

		s.logger.Info("starting kiri HTTP server", "addr", s.Addr())

		ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", s.Addr())
		if err != nil {
			// Close httpReady so the combiner goroutine doesn't hang,
			// and surface the error to the caller via startErr.
			s.logger.Error("http listen failed", "error", err)
			close(httpReady)
			startErr <- fmt.Errorf("http listen on %s: %w", s.Addr(), err)
			return
		}

		// Signal that the listener is up; subsequent serve errors go to startErr
		close(httpReady)

		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.logger.Error("http serve error", "error", err)
			startErr <- fmt.Errorf("http serve: %w", err)
		}
	}()

	go func() {
		if s.config.GRPCPort > 0 {
			if err := s.grpc.Start(grpcReady); err != nil {
				s.logger.Error("grpc serve error", "error", err)
				startErr <- fmt.Errorf("grpc serve: %w", err)
			}
		} else {
			close(grpcReady)
		}
	}()

	// Wait for both listeners to be ready or for the first startup error.
	// Closing combinedReady drives the nil-return path; startErr drives
	// the early-error path. readyCh (when provided) is closed once both
	// signals fire, mirroring the previous behaviour for callers like
	// Run() that block on readiness.
	combinedReady := make(chan struct{})
	go func() {
		<-httpReady
		<-grpcReady
		close(combinedReady)
		if len(readyCh) > 0 && readyCh[0] != nil {
			close(readyCh[0])
		}
	}()

	select {
	case <-combinedReady:
		return nil
	case err := <-startErr:
		return err
	}
}

// Shutdown drains in-flight requests, saves persistent snapshots, and stops
// the gRPC server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("shutting down")

	if s.http != nil {
		if err := s.http.Shutdown(ctx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}

	s.grpc.GracefulStop()

	for _, svc := range s.registry.All() {
		if c, ok := svc.(io.Closer); ok {
			if err := c.Close(); err != nil {
				s.logger.Error("save snapshot failed", "service", svc.Name(), "error", err)
			}
		}
	}

	return nil
}

// Run starts the server and blocks until SIGINT/SIGTERM, then shuts down.
func (s *Server) Run() error {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	errChan := make(chan error, 1)
	readyCh := make(chan struct{})

	go func() {
		if err := s.Start(readyCh); err != nil {
			errChan <- err
		}
	}()

	select {
	case <-readyCh:
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	}

	select {
	case sig := <-sigChan:
		s.logger.Info("received signal", "signal", sig.String())
	case err := <-errChan:
		return fmt.Errorf("server error: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return s.Shutdown(ctx)
}
