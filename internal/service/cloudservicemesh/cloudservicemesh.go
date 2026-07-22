// Package cloudservicemesh emulates Cloud Service Mesh (a subset of
// networkservices.googleapis.com/v1): meshes and the routes attached to them.
package cloudservicemesh

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "cloudservicemesh"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type httpRoute struct {
	Name  string   `json:"name"`
	Hosts []string `json:"hostnames,omitempty"`
}

type mesh struct {
	Name   string                `json:"name"`
	Routes map[string]*httpRoute `json:"routes"`
}

type state struct {
	Meshes map[string]*mesh `json:"meshes"` // full path -> mesh
}

// Service implements the Cloud Service Mesh emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Service Mesh store.
func New() *Service { return &Service{st: state{Meshes: map[string]*mesh{}}} }

func (s *Service) ensureMaps() {
	if s.st.Meshes == nil {
		s.st.Meshes = map[string]*mesh{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Service Mesh",
		Category:    "Containers",
		Description: "Managed Istio-based service mesh and HTTP routes",
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

// RegisterRoutes registers the Cloud Service Mesh REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/meshes"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{mesh}", s.get)
	r.Handle("DELETE", base+"/{mesh}", s.delete)

	routeBase := base + "/{mesh}/httpRoutes"
	r.Handle("POST", routeBase, s.createRoute)
	r.Handle("GET", routeBase, s.listRoutes)
	r.Handle("DELETE", routeBase+"/{route}", s.deleteRoute)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/meshes/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body mesh
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r) + body.Name
	body.Routes = map[string]*httpRoute{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Meshes[name]; exists {
		httpx.AlreadyExists(w, "mesh already exists: "+name)

		return
	}

	s.st.Meshes[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Meshes {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*mesh, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Meshes[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"meshes": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("mesh")

	s.mu.RLock()
	m, ok := s.st.Meshes[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "mesh not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, m)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("mesh")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Meshes[name]; !ok {
		httpx.NotFound(w, "mesh not found: "+name)

		return
	}

	delete(s.st.Meshes, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) meshName(r *http.Request) string {
	return s.prefix(r) + r.PathValue("mesh")
}

func (s *Service) createRoute(w http.ResponseWriter, r *http.Request) {
	var body httpRoute
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.st.Meshes[s.meshName(r)]
	if !ok {
		httpx.NotFound(w, "mesh not found: "+s.meshName(r))

		return
	}

	if _, exists := m.Routes[body.Name]; exists {
		httpx.AlreadyExists(w, "route already exists: "+body.Name)

		return
	}

	m.Routes[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listRoutes(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, ok := s.st.Meshes[s.meshName(r)]
	if !ok {
		httpx.NotFound(w, "mesh not found: "+s.meshName(r))

		return
	}

	names := make([]string, 0, len(m.Routes))
	for n := range m.Routes {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*httpRoute, 0, len(names))
	for _, n := range names {
		items = append(items, m.Routes[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"httpRoutes": items})
}

func (s *Service) deleteRoute(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.st.Meshes[s.meshName(r)]
	if !ok {
		httpx.NotFound(w, "mesh not found: "+s.meshName(r))

		return
	}

	if _, ok := m.Routes[r.PathValue("route")]; !ok {
		httpx.NotFound(w, "route not found: "+r.PathValue("route"))

		return
	}

	delete(m.Routes, r.PathValue("route"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
