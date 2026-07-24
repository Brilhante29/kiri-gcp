// Package gke emulates Google Kubernetes Engine (container.googleapis.com/v1):
// clusters and their node pools.
package gke

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "gke"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type nodePool struct {
	Name             string `json:"name"`
	InitialNodeCount int    `json:"initialNodeCount,omitempty"`
	Status           string `json:"status"`
}

type cluster struct {
	Name             string               `json:"name"`
	InitialNodeCount int                  `json:"initialNodeCount,omitempty"`
	Status           string               `json:"status"`
	Location         string               `json:"location,omitempty"`
	NodePools        map[string]*nodePool `json:"nodePools"`
}

type state struct {
	Clusters map[string]*cluster `json:"clusters"` // full path -> cluster
}

// Service implements the GKE emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty GKE store.
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
		Display:     "GKE",
		Category:    "Containers",
		Description: "Managed Kubernetes clusters and node pools",
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

// RegisterRoutes registers the GKE REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/clusters"
	r.Handle("POST", base, s.createCluster)
	r.Handle("GET", base, s.listClusters)
	r.Handle("GET", base+"/{cluster}", s.getCluster)
	r.Handle("DELETE", base+"/{cluster}", s.deleteCluster)

	npBase := base + "/{cluster}/nodePools"
	r.Handle("POST", npBase, s.createNodePool)
	r.Handle("GET", npBase, s.listNodePools)
	r.Handle("GET", npBase+"/{nodepool}", s.getNodePool)
	r.Handle("DELETE", npBase+"/{nodepool}", s.deleteNodePool)
}

func (s *Service) clusterName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/clusters/" + r.PathValue("cluster")
}

func (s *Service) clusterPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/clusters/"
}

// ---- Clusters ----

func (s *Service) createCluster(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	// The real API nests the resource under a "cluster" body field, but this
	// emulator also accepts a flat body for convenience.
	var c cluster
	_ = json.Unmarshal(raw, &c)

	if c.Name == "" {
		var nested struct {
			Cluster cluster `json:"cluster"`
		}
		_ = json.Unmarshal(raw, &nested)
		c = nested.Cluster
	}

	if c.Name == "" {
		httpx.BadRequest(w, "name (or cluster.name) is required")

		return
	}

	name := s.clusterPrefix(r) + c.Name
	c.Location = r.PathValue("location")
	c.Status = "RUNNING"
	c.NodePools = map[string]*nodePool{}

	if c.InitialNodeCount == 0 {
		c.InitialNodeCount = 1
	}

	c.NodePools["default-pool"] = &nodePool{Name: "default-pool", InitialNodeCount: c.InitialNodeCount, Status: "RUNNING"}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Clusters[name]; exists {
		httpx.AlreadyExists(w, "cluster already exists: "+name)

		return
	}

	s.st.Clusters[name] = &c

	httpx.WriteJSON(w, http.StatusOK, c)
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
	name := s.clusterName(r)

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
	name := s.clusterName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Clusters[name]; !ok {
		httpx.NotFound(w, "cluster not found: "+name)

		return
	}

	delete(s.st.Clusters, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Node pools ----

func (s *Service) createNodePool(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	// The real API nests the resource under a "nodePool" body field, but
	// this emulator also accepts a flat body for convenience.
	var np nodePool
	_ = json.Unmarshal(raw, &np)

	if np.Name == "" {
		var nested struct {
			NodePool nodePool `json:"nodePool"`
		}
		_ = json.Unmarshal(raw, &nested)
		np = nested.NodePool
	}

	if np.Name == "" {
		httpx.BadRequest(w, "name (or nodePool.name) is required")

		return
	}

	np.Status = "RUNNING"

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, exists := c.NodePools[np.Name]; exists {
		httpx.AlreadyExists(w, "node pool already exists: "+np.Name)

		return
	}

	c.NodePools[np.Name] = &np

	httpx.WriteJSON(w, http.StatusOK, np)
}

func (s *Service) listNodePools(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	names := make([]string, 0, len(c.NodePools))
	for n := range c.NodePools {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*nodePool, 0, len(names))
	for _, n := range names {
		items = append(items, c.NodePools[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"nodePools": items})
}

func (s *Service) getNodePool(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	np, ok := c.NodePools[r.PathValue("nodepool")]
	if !ok {
		httpx.NotFound(w, "node pool not found: "+r.PathValue("nodepool"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, np)
}

func (s *Service) deleteNodePool(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, ok := c.NodePools[r.PathValue("nodepool")]; !ok {
		httpx.NotFound(w, "node pool not found: "+r.PathValue("nodepool"))

		return
	}

	delete(c.NodePools, r.PathValue("nodepool"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
