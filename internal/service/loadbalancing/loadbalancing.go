// Package loadbalancing emulates regional Cloud Load Balancing resources
// (compute.googleapis.com/v1): backend services and health checks.
package loadbalancing

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "loadbalancing"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type healthCheck struct {
	Name string `json:"name"`
	Type string `json:"type,omitempty"`
	Port int    `json:"port,omitempty"`
}

type backendService struct {
	Name         string           `json:"name"`
	Protocol     string           `json:"protocol,omitempty"`
	HealthChecks []string         `json:"healthChecks,omitempty"`
	Backends     []map[string]any `json:"backends,omitempty"`
}

type state struct {
	BackendServices map[string]*backendService `json:"backendServices"`
	HealthChecks    map[string]*healthCheck    `json:"healthChecks"`
}

// Service implements the regional load balancing emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty load balancing store.
func New() *Service {
	return &Service{st: state{
		BackendServices: map[string]*backendService{},
		HealthChecks:    map[string]*healthCheck{},
	}}
}

func (s *Service) ensureMaps() {
	if s.st.BackendServices == nil {
		s.st.BackendServices = map[string]*backendService{}
	}

	if s.st.HealthChecks == nil {
		s.st.HealthChecks = map[string]*healthCheck{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Load Balancing",
		Category:    "Networking",
		Description: "Regional backend services and health checks",
		Fidelity:    service.FidelityB,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the load balancing REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	beBase := "/compute/v1/projects/{project}/regions/{region}/backendServices"
	r.Handle("POST", beBase, s.createBackendService)
	r.Handle("GET", beBase, s.listBackendServices)
	r.Handle("GET", beBase+"/{name}", s.getBackendService)
	r.Handle("DELETE", beBase+"/{name}", s.deleteBackendService)

	hcBase := "/compute/v1/projects/{project}/regions/{region}/healthChecks"
	r.Handle("POST", hcBase, s.createHealthCheck)
	r.Handle("GET", hcBase, s.listHealthChecks)
	r.Handle("GET", hcBase+"/{name}", s.getHealthCheck)
	r.Handle("DELETE", hcBase+"/{name}", s.deleteHealthCheck)
}

func (s *Service) bePrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/backendServices/"
}

func (s *Service) hcPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/healthChecks/"
}

// ---- Backend services ----

func (s *Service) createBackendService(w http.ResponseWriter, r *http.Request) {
	var body backendService
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.bePrefix(r) + body.Name
	if body.Protocol == "" {
		body.Protocol = "HTTP"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.BackendServices[name]; exists {
		httpx.AlreadyExists(w, "backend service already exists: "+name)

		return
	}

	s.st.BackendServices[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listBackendServices(w http.ResponseWriter, r *http.Request) {
	prefix := s.bePrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.BackendServices {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*backendService, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.BackendServices[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getBackendService(w http.ResponseWriter, r *http.Request) {
	name := s.bePrefix(r) + r.PathValue("name")

	s.mu.RLock()
	be, ok := s.st.BackendServices[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "backend service not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, be)
}

func (s *Service) deleteBackendService(w http.ResponseWriter, r *http.Request) {
	name := s.bePrefix(r) + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.BackendServices[name]; !ok {
		httpx.NotFound(w, "backend service not found: "+name)

		return
	}

	delete(s.st.BackendServices, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Health checks ----

func (s *Service) createHealthCheck(w http.ResponseWriter, r *http.Request) {
	var body healthCheck
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.hcPrefix(r) + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.HealthChecks[name]; exists {
		httpx.AlreadyExists(w, "health check already exists: "+name)

		return
	}

	s.st.HealthChecks[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listHealthChecks(w http.ResponseWriter, r *http.Request) {
	prefix := s.hcPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.HealthChecks {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*healthCheck, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.HealthChecks[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getHealthCheck(w http.ResponseWriter, r *http.Request) {
	name := s.hcPrefix(r) + r.PathValue("name")

	s.mu.RLock()
	hc, ok := s.st.HealthChecks[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "health check not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, hc)
}

func (s *Service) deleteHealthCheck(w http.ResponseWriter, r *http.Request) {
	name := s.hcPrefix(r) + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.HealthChecks[name]; !ok {
		httpx.NotFound(w, "health check not found: "+name)

		return
	}

	delete(s.st.HealthChecks, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
