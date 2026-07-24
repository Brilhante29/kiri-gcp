// Package cloudrun emulates Cloud Run (run.googleapis.com/v1): services with
// container image, environment variables, and a tracked latest revision.
package cloudrun

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "cloudrun"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type runService struct {
	Name           string `json:"name"`
	Image          string `json:"image,omitempty"`
	LatestRevision string `json:"latestRevision"`
	URL            string `json:"url,omitempty"`
	Status         string `json:"status"`
}

type state struct {
	Services map[string]*runService `json:"services"` // full path -> service
}

// Service implements the Cloud Run emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Run store.
func New() *Service { return &Service{st: state{Services: map[string]*runService{}}} }

func (s *Service) ensureMaps() {
	if s.st.Services == nil {
		s.st.Services = map[string]*runService{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Run",
		Category:    "Containers",
		Description: "Fully managed serverless containers",
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

// RegisterRoutes registers the Cloud Run REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/services"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{svc}", s.get)
	r.Handle("PUT", base+"/{svc}", s.update)
	r.Handle("DELETE", base+"/{svc}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/services/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body runService
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = "service-" + httpx.ID(4)
	}

	name := s.prefix(r) + body.Name
	body.Status = "READY"
	body.LatestRevision = body.Name + "-00001"
	body.URL = "https://" + body.Name + "-uc.a.run.app"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Services[name]; exists {
		httpx.AlreadyExists(w, "service already exists: "+name)

		return
	}

	s.st.Services[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Services {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*runService, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Services[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("svc")

	s.mu.RLock()
	svc, ok := s.st.Services[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, svc)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("svc")

	s.mu.Lock()
	defer s.mu.Unlock()

	svc, ok := s.st.Services[name]
	if !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	var patch runService
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if patch.Image != "" {
		svc.Image = patch.Image
		svc.LatestRevision = r.PathValue("svc") + "-" + httpx.ID(2)
	}

	httpx.WriteJSON(w, http.StatusOK, svc)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("svc")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Services[name]; !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	delete(s.st.Services, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
