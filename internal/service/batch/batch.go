// Package batch emulates Cloud Batch (batch.googleapis.com/v1): job
// scheduling and execution.
//
// Routes are prefixed with "/batch" — real GCP would serve this at the same
// "/v1/projects/{p}/locations/{l}/jobs" path "cloudscheduler" already owns
// here, disambiguated in production only by API host (batch.googleapis.com
// vs cloudscheduler.googleapis.com). No real alternate API version is known
// for Cloud Batch, so — same as managedkafka — this prefix is an
// emulator-only convention, not a byte-real path. Reachable via direct REST;
// not a drop-in target for an unmodified Cloud Batch client SDK.
package batch

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "batch"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type job struct {
	Name       string           `json:"name"`
	TaskGroups []map[string]any `json:"taskGroups,omitempty"`
	Status     map[string]any   `json:"status"`
}

type state struct {
	Jobs map[string]*job `json:"jobs"` // full path -> job
}

// Service implements the Cloud Batch emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Batch store.
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
		Display:     "Batch",
		Category:    "Compute",
		Description: "Managed batch job scheduling and execution",
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

// RegisterRoutes registers the Cloud Batch REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/batch/v1/projects/{project}/locations/{location}/jobs"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{job}", s.get)
	r.Handle("DELETE", base+"/{job}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/jobs/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	jobID := r.URL.Query().Get("jobId")

	var body job
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if jobID == "" {
		jobID = "job-" + httpx.ID(4)
	}

	name := s.prefix(r) + jobID
	body.Name = name
	body.Status = map[string]any{"state": "SUCCEEDED"}

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
