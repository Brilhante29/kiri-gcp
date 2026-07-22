// Package dataproc emulates Dataproc (dataproc.googleapis.com/v1): managed
// Spark/Hadoop clusters and the jobs submitted to them.
package dataproc

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "dataproc"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type cluster struct {
	ClusterName string         `json:"clusterName"`
	Config      map[string]any `json:"config,omitempty"`
	Status      map[string]any `json:"status"`
}

type job struct {
	Reference map[string]any `json:"reference,omitempty"`
	Placement map[string]any `json:"placement,omitempty"`
	Status    map[string]any `json:"status"`
}

type state struct {
	Clusters map[string]*cluster `json:"clusters"` // full path -> cluster
	Jobs     map[string]*job     `json:"jobs"`      // full path -> job
}

// Service implements the Dataproc emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Dataproc store.
func New() *Service {
	return &Service{st: state{Clusters: map[string]*cluster{}, Jobs: map[string]*job{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Clusters == nil {
		s.st.Clusters = map[string]*cluster{}
	}

	if s.st.Jobs == nil {
		s.st.Jobs = map[string]*job{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Dataproc",
		Category:    "Analytics & ML",
		Description: "Managed Spark/Hadoop clusters and jobs",
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

// RegisterRoutes registers the Dataproc REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/regions/{region}/clusters"
	r.Handle("POST", base, s.createCluster)
	r.Handle("GET", base, s.listClusters)
	r.Handle("GET", base+"/{cluster}", s.getCluster)
	r.Handle("DELETE", base+"/{cluster}", s.deleteCluster)

	jobBase := "/v1/projects/{project}/regions/{region}/jobs"
	// POST on the collection only makes sense as the ":submit" custom
	// method (real API: POST .../jobs:submit).
	r.Handle("POST", jobBase, s.submitJob)
	r.Handle("GET", jobBase, s.listJobs)
	r.Handle("GET", jobBase+"/{job}", s.getJob)
	// {job} also matches "jobId:cancel".
	r.Handle("POST", jobBase+"/{job}", s.jobAction)
}

func (s *Service) clusterPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/clusters/"
}

func (s *Service) jobPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/jobs/"
}

// ---- Clusters ----

func (s *Service) createCluster(w http.ResponseWriter, r *http.Request) {
	var body cluster
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.ClusterName == "" {
		httpx.BadRequest(w, "clusterName is required")

		return
	}

	name := s.clusterPrefix(r) + body.ClusterName
	body.Status = map[string]any{"state": "RUNNING"}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Clusters[name]; exists {
		httpx.AlreadyExists(w, "cluster already exists: "+name)

		return
	}

	s.st.Clusters[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listClusters(w http.ResponseWriter, r *http.Request) {
	prefix := s.clusterPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Clusters {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*cluster, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Clusters[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"clusters": items})
}

func (s *Service) getCluster(w http.ResponseWriter, r *http.Request) {
	name := s.clusterPrefix(r) + r.PathValue("cluster")

	s.mu.RLock()
	c, ok := s.st.Clusters[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "cluster not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) deleteCluster(w http.ResponseWriter, r *http.Request) {
	name := s.clusterPrefix(r) + r.PathValue("cluster")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Clusters[name]; !ok {
		httpx.NotFound(w, "cluster not found: "+name)

		return
	}

	delete(s.st.Clusters, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Jobs ----

func (s *Service) submitJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Job job `json:"job"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	j := body.Job
	jobID := httpx.ID(8)
	name := s.jobPrefix(r) + jobID
	j.Status = map[string]any{"state": "RUNNING"}

	if j.Reference == nil {
		j.Reference = map[string]any{}
	}

	j.Reference["jobId"] = jobID

	s.mu.Lock()
	s.st.Jobs[name] = &j
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, j)
}

func (s *Service) listJobs(w http.ResponseWriter, r *http.Request) {
	prefix := s.jobPrefix(r)

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
	name := s.jobPrefix(r) + r.PathValue("job")

	s.mu.RLock()
	j, ok := s.st.Jobs[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, j)
}

func (s *Service) jobAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("job"))
	if verb != "cancel" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	name := s.jobPrefix(r) + id

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.st.Jobs[name]
	if !ok {
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	j.Status = map[string]any{"state": "CANCELLED"}

	httpx.WriteJSON(w, http.StatusOK, j)
}
