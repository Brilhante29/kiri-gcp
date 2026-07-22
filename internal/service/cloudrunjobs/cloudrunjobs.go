// Package cloudrunjobs emulates Cloud Run Jobs (run.googleapis.com/v2):
// run-to-completion jobs where each ":run" call creates a tracked execution.
// Uses the v2 Cloud Run Admin API path shape (matching this codebase's
// "cloudrun" services package) — v1 was avoided because
// "/v1/projects/{p}/locations/{l}/jobs" collides byte-for-byte with Cloud
// Scheduler's job resource path; in real GCP the two are disambiguated by
// API host (run.googleapis.com vs cloudscheduler.googleapis.com), which
// this emulator's flat path router does not model.
package cloudrunjobs

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "cloudrunjobs"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type execution struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type job struct {
	Name       string                `json:"name"`
	Image      string                `json:"image,omitempty"`
	Executions map[string]*execution `json:"executions"`
}

type state struct {
	Jobs map[string]*job `json:"jobs"` // full path -> job
}

// Service implements the Cloud Run Jobs emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Run Jobs store.
func New() *Service { return &Service{st: state{Jobs: map[string]*job{}}} }

func (s *Service) ensureMaps() {
	if s.st.Jobs == nil {
		s.st.Jobs = map[string]*job{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Run Jobs",
		Category:    "Containers",
		Description: "Run-to-completion containerized jobs and their executions",
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

// RegisterRoutes registers the Cloud Run Jobs REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v2/projects/{project}/locations/{location}/jobs"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{job}", s.get)
	// {job} also matches "name:run".
	r.Handle("POST", base+"/{job}", s.jobAction)
	r.Handle("DELETE", base+"/{job}", s.delete)

	r.Handle("GET", base+"/{job}/executions", s.listExecutions)
	r.Handle("GET", base+"/{job}/executions/{execution}", s.getExecution)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/jobs/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body job
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = "job-" + httpx.ID(4)
	}

	name := s.prefix(r) + body.Name
	body.Executions = map[string]*execution{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Jobs[name]; exists {
		httpx.AlreadyExists(w, "job already exists: "+name)

		return
	}

	s.st.Jobs[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Jobs {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*job, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Jobs[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("job")

	s.mu.RLock()
	j, ok := s.st.Jobs[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, j)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("job")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Jobs[name]; !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	delete(s.st.Jobs, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) jobAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("job"))
	if verb != "run" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	name := s.prefix(r) + id

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.st.Jobs[name]
	if !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	execID := httpx.ID(6)
	exec := &execution{Name: name + "/executions/" + execID, State: "SUCCEEDED"}
	j.Executions[execID] = exec

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"execution": exec})
}

func (s *Service) listExecutions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.st.Jobs[s.prefix(r)+r.PathValue("job")]
	if !ok {
		httpx.NotFound(w, "job not found")

		return
	}

	ids := make([]string, 0, len(j.Executions))
	for id := range j.Executions {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*execution, 0, len(ids))
	for _, id := range ids {
		items = append(items, j.Executions[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"executions": items})
}

func (s *Service) getExecution(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.st.Jobs[s.prefix(r)+r.PathValue("job")]
	if !ok {
		httpx.NotFound(w, "job not found")

		return
	}

	e, ok := j.Executions[r.PathValue("execution")]
	if !ok {
		httpx.NotFound(w, "execution not found: "+r.PathValue("execution"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, e)
}
