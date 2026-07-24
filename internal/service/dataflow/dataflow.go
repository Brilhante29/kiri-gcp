// Package dataflow emulates Dataflow (dataflow.googleapis.com/v1b3): jobs
// and their state transitions (cancel/drain via PUT requestedState, matching
// the real API).
package dataflow

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "dataflow"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type job struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Type         string         `json:"type,omitempty"`
	CurrentState string         `json:"currentState"`
	Environment  map[string]any `json:"environment,omitempty"`
}

type state struct {
	Jobs map[string]*job `json:"jobs"` // full path -> job
}

// Service implements the Dataflow emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Dataflow store.
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
		Display:     "Dataflow",
		Category:    "Analytics & ML",
		Description: "Managed stream and batch data processing jobs",
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

// RegisterRoutes registers the Dataflow REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1b3/projects/{project}/locations/{location}/jobs"
	r.Handle("POST", base, s.createJob)
	r.Handle("GET", base, s.listJobs)
	r.Handle("GET", base+"/{job}", s.getJob)
	r.Handle("PUT", base+"/{job}", s.updateJob)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/jobs/"
}

func (s *Service) createJob(w http.ResponseWriter, r *http.Request) {
	var body job
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = "job-" + httpx.ID(4)
	}

	id := httpx.ID(8)
	body.ID = id
	body.CurrentState = "JOB_STATE_RUNNING"

	s.mu.Lock()
	s.st.Jobs[s.prefix(r)+id] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listJobs(w http.ResponseWriter, r *http.Request) {
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

func (s *Service) getJob(w http.ResponseWriter, r *http.Request) {
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

// updateJob applies a requestedState transition — the real API's mechanism
// for cancelling ("JOB_STATE_CANCELLED") or draining ("JOB_STATE_DRAINED") a
// running job.
func (s *Service) updateJob(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("job")

	var body struct {
		RequestedState string `json:"requestedState"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.st.Jobs[name]
	if !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	if body.RequestedState != "" {
		j.CurrentState = body.RequestedState
	}

	httpx.WriteJSON(w, http.StatusOK, j)
}
