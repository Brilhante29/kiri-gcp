// Package resourcemanager emulates Resource Manager
// (cloudresourcemanager.googleapis.com/v1): project lifecycle and labels.
package resourcemanager

import (
	"net/http"
	"sort"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "resourcemanager"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	svc.seedDefault()
	service.Register(svc)
}

type project struct {
	ProjectID string            `json:"projectId"`
	Name      string            `json:"name,omitempty"`
	State     string            `json:"state"`
	Labels    map[string]string `json:"labels,omitempty"`
}

type state struct {
	Projects map[string]*project `json:"projects"` // projectId -> project
}

// Service implements the Resource Manager emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Resource Manager store.
func New() *Service { return &Service{st: state{Projects: map[string]*project{}}} }

func (s *Service) ensureMaps() {
	if s.st.Projects == nil {
		s.st.Projects = map[string]*project{}
	}
}

// seedDefault ensures the conventional "kiri-project" demo project
// always exists, matching prior behavior that other tests may rely on.
func (s *Service) seedDefault() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Projects["kiri-project"]; !ok {
		s.st.Projects["kiri-project"] = &project{ProjectID: "kiri-project", State: "ACTIVE"}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Resource Manager",
		Category:    "Management & Billing",
		Description: "Project lifecycle and labels",
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

// RegisterRoutes registers the Resource Manager REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("GET", "/v1/projects", s.listProjects)
	r.Handle("POST", "/v1/projects", s.createProject)
	r.Handle("GET", "/v1/projects/{id}", s.getProject)
	r.Handle("PUT", "/v1/projects/{id}/labels", s.updateLabels)
	r.Handle("DELETE", "/v1/projects/{id}", s.deleteProject)
}

func (s *Service) listProjects(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids := make([]string, 0, len(s.st.Projects))
	for id := range s.st.Projects {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*project, 0, len(ids))
	for _, id := range ids {
		items = append(items, s.st.Projects[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"projects": items})
}

func (s *Service) createProject(w http.ResponseWriter, r *http.Request) {
	var body project
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.ProjectID == "" {
		httpx.BadRequest(w, "projectId is required")

		return
	}

	body.State = "ACTIVE"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Projects[body.ProjectID]; exists {
		httpx.AlreadyExists(w, "project already exists: "+body.ProjectID)

		return
	}

	s.st.Projects[body.ProjectID] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) getProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.RLock()
	p, ok := s.st.Projects[id]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "project not found: "+id)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) updateLabels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var body struct {
		Labels map[string]string `json:"labels"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.st.Projects[id]
	if !ok {
		httpx.NotFound(w, "project not found: "+id)

		return
	}

	p.Labels = body.Labels

	httpx.WriteJSON(w, http.StatusOK, p)
}

// deleteProject marks the project for deletion (real GCP schedules a 30-day
// grace period rather than deleting immediately) and responds 204, matching
// the REST convention used elsewhere in this emulator for resource deletes.
func (s *Service) deleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.st.Projects[id]
	if !ok {
		httpx.NotFound(w, "project not found: "+id)

		return
	}

	p.State = "DELETE_REQUESTED"

	w.WriteHeader(http.StatusNoContent)
}
