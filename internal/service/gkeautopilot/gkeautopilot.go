// Package gkeautopilot emulates GKE Autopilot (container.googleapis.com):
// clusters only — unlike standard GKE, Autopilot manages nodes automatically
// and does not expose a node pool API to callers.
//
// In real GCP, Autopilot is a mode of the same container.googleapis.com/v1
// API the "gke" package emulates (selected via an "autopilot: true" field on
// the same clusters.create call), not a separate endpoint. This emulator
// keeps GKE and GKE Autopilot as independently registered catalog services,
// so v1beta1 (also a real, distinct GKE API surface) is used here instead —
// v1 would collide byte-for-byte with "gke"'s route and one service would
// silently shadow the other in this router's flat path namespace.
package gkeautopilot

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "gkeautopilot"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type cluster struct {
	Name      string `json:"name"`
	Status    string `json:"status"`
	Location  string `json:"location,omitempty"`
	Autopilot struct {
		Enabled bool `json:"enabled"`
	} `json:"autopilot"`
}

type state struct {
	Clusters map[string]*cluster `json:"clusters"` // full path -> cluster
}

// Service implements the GKE Autopilot emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty GKE Autopilot store.
func New() *Service { return &Service{st: state{Clusters: map[string]*cluster{}}} }

func (s *Service) ensureMaps() {
	if s.st.Clusters == nil {
		s.st.Clusters = map[string]*cluster{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "GKE Autopilot",
		Category:    "Containers",
		Description: "Fully managed, hands-off Kubernetes clusters",
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

// RegisterRoutes registers the GKE Autopilot REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1beta1/projects/{project}/locations/{location}/clusters"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{cluster}", s.get)
	r.Handle("DELETE", base+"/{cluster}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/clusters/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Cluster cluster `json:"cluster"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	c := body.Cluster
	if c.Name == "" {
		httpx.BadRequest(w, "cluster.name is required")

		return
	}

	name := s.prefix(r) + c.Name
	c.Location = r.PathValue("location")
	c.Status = "RUNNING"
	c.Autopilot.Enabled = true

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Clusters[name]; exists {
		httpx.AlreadyExists(w, "cluster already exists: "+name)

		return
	}

	s.st.Clusters[name] = &c

	httpx.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

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

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("cluster")

	s.mu.RLock()
	c, ok := s.st.Clusters[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "cluster not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("cluster")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Clusters[name]; !ok {
		httpx.NotFound(w, "cluster not found: "+name)

		return
	}

	delete(s.st.Clusters, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
