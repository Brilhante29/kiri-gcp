// Package memorystore emulates Memorystore for Redis
// (redis.googleapis.com/v1): Redis instances with basic lifecycle actions.
package memorystore

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "memorystore"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type instance struct {
	Name         string `json:"name"`
	Tier         string `json:"tier,omitempty"`
	MemorySizeGb int    `json:"memorySizeGb,omitempty"`
	Host         string `json:"host,omitempty"`
	Port         int    `json:"port,omitempty"`
	State        string `json:"state"`
}

type state struct {
	Instances map[string]*instance `json:"instances"` // full path -> instance
}

// Service implements the Memorystore for Redis emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Memorystore store.
func New() *Service { return &Service{st: state{Instances: map[string]*instance{}}} }

func (s *Service) ensureMaps() {
	if s.st.Instances == nil {
		s.st.Instances = map[string]*instance{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Memorystore for Redis",
		Category:    "Databases",
		Description: "Managed Redis instances",
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

// RegisterRoutes registers the Memorystore REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/instances"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{instance}", s.get)
	r.Handle("DELETE", base+"/{instance}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/instances/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body instance
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r) + body.Name
	if body.Tier == "" {
		body.Tier = "BASIC"
	}

	if body.MemorySizeGb == 0 {
		body.MemorySizeGb = 1
	}

	body.State = "READY"
	body.Host = "10.0." + httpx.NumericID()[:2] + "." + httpx.NumericID()[:3]
	body.Port = 6379

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Instances[name]; exists {
		httpx.AlreadyExists(w, "instance already exists: "+name)

		return
	}

	s.st.Instances[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Instances {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*instance, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Instances[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"instances": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("instance")

	s.mu.RLock()
	i, ok := s.st.Instances[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, i)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("instance")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Instances[name]; !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	delete(s.st.Instances, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
