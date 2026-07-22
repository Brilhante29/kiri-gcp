// Package cloudcomposer emulates Cloud Composer (composer.googleapis.com/v1):
// managed Apache Airflow environments.
package cloudcomposer

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "cloudcomposer"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type environment struct {
	Name       string `json:"name"`
	State      string `json:"state"`
	AirflowURI string `json:"airflowUri,omitempty"`
}

type state struct {
	Environments map[string]*environment `json:"environments"` // full path -> environment
}

// Service implements the Cloud Composer emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Composer store.
func New() *Service { return &Service{st: state{Environments: map[string]*environment{}}} }

func (s *Service) ensureMaps() {
	if s.st.Environments == nil {
		s.st.Environments = map[string]*environment{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Composer",
		Category:    "Application Integration",
		Description: "Managed Apache Airflow environments",
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

// RegisterRoutes registers the Cloud Composer REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/environments"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{environment}", s.get)
	r.Handle("DELETE", base+"/{environment}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/environments/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body environment
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r) + body.Name
	body.State = "RUNNING"
	body.AirflowURI = "https://" + body.Name + "-composer.appspot.com"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Environments[name]; exists {
		httpx.AlreadyExists(w, "environment already exists: "+name)

		return
	}

	s.st.Environments[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Environments {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*environment, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Environments[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"environments": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("environment")

	s.mu.RLock()
	e, ok := s.st.Environments[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "environment not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, e)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("environment")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Environments[name]; !ok {
		httpx.NotFound(w, "environment not found: "+name)

		return
	}

	delete(s.st.Environments, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
