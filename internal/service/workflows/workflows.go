// Package workflows emulates Workflows (workflows.googleapis.com/v1):
// workflow definitions and their executions.
package workflows

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "workflows"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type execution struct {
	Name   string `json:"name"`
	State  string `json:"state"`
	Result string `json:"result,omitempty"`
}

type workflow struct {
	Name           string                `json:"name"`
	SourceContents string                `json:"sourceContents,omitempty"`
	State          string                `json:"state"`
	Executions     map[string]*execution `json:"executions"`
}

type state struct {
	Workflows map[string]*workflow `json:"workflows"` // full path -> workflow
}

// Service implements the Workflows emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Workflows store.
func New() *Service { return &Service{st: state{Workflows: map[string]*workflow{}}} }

func (s *Service) ensureMaps() {
	if s.st.Workflows == nil {
		s.st.Workflows = map[string]*workflow{}
	}

	for _, wf := range s.st.Workflows {
		if wf.Executions == nil {
			wf.Executions = map[string]*execution{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Workflows",
		Category:    "Application Integration",
		Description: "Workflow definitions and their executions",
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

// RegisterRoutes registers the Workflows REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/workflows"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{workflow}", s.get)
	r.Handle("DELETE", base+"/{workflow}", s.delete)

	execBase := base + "/{workflow}/executions"
	r.Handle("POST", execBase, s.createExecution)
	r.Handle("GET", execBase, s.listExecutions)
	r.Handle("GET", execBase+"/{execution}", s.getExecution)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/workflows/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body workflow
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r) + body.Name
	body.State = "ACTIVE"
	body.Executions = map[string]*execution{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Workflows[name]; exists {
		httpx.AlreadyExists(w, "workflow already exists: "+name)

		return
	}

	s.st.Workflows[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Workflows {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*workflow, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Workflows[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"workflows": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("workflow")

	s.mu.RLock()
	wf, ok := s.st.Workflows[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "workflow not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, wf)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("workflow")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Workflows[name]; !ok {
		httpx.NotFound(w, "workflow not found: "+name)

		return
	}

	delete(s.st.Workflows, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createExecution(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	wf, ok := s.st.Workflows[s.prefix(r)+r.PathValue("workflow")]
	if !ok {
		httpx.NotFound(w, "workflow not found")

		return
	}

	id := httpx.ID(8)
	exec := &execution{Name: wf.Name + "/executions/" + id, State: "SUCCEEDED", Result: "{}"}
	wf.Executions[id] = exec

	httpx.WriteJSON(w, http.StatusOK, exec)
}

func (s *Service) listExecutions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wf, ok := s.st.Workflows[s.prefix(r)+r.PathValue("workflow")]
	if !ok {
		httpx.NotFound(w, "workflow not found")

		return
	}

	ids := make([]string, 0, len(wf.Executions))
	for id := range wf.Executions {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*execution, 0, len(ids))
	for _, id := range ids {
		items = append(items, wf.Executions[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"executions": items})
}

func (s *Service) getExecution(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	wf, ok := s.st.Workflows[s.prefix(r)+r.PathValue("workflow")]
	if !ok {
		httpx.NotFound(w, "workflow not found")

		return
	}

	e, ok := wf.Executions[r.PathValue("execution")]
	if !ok {
		httpx.NotFound(w, "execution not found: "+r.PathValue("execution"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, e)
}
