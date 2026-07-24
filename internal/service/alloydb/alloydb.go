// Package alloydb emulates AlloyDB (alloydb.googleapis.com): clusters and
// the primary/read-pool instances within them.
//
// Uses v1beta (also a real, valid AlloyDB API surface) instead of v1: v1
// collides byte-for-byte with "gke"'s "/v1/projects/{p}/locations/{l}/clusters"
// route — real GCP disambiguates by API host (alloydb.googleapis.com vs
// container.googleapis.com), which this emulator's flat path router does not
// model. Same class of fix as gkeautopilot's v1beta1 and cloudrunjobs' v2.
package alloydb

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "alloydb"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type instance struct {
	Name         string `json:"name"`
	InstanceType string `json:"instanceType,omitempty"`
	State        string `json:"state"`
}

type cluster struct {
	Name      string               `json:"name"`
	State     string               `json:"state"`
	Instances map[string]*instance `json:"instances"`
}

type state struct {
	Clusters map[string]*cluster `json:"clusters"` // full path -> cluster
}

// Service implements the AlloyDB emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty AlloyDB store.
func New() *Service { return &Service{st: state{Clusters: map[string]*cluster{}}} }

func (s *Service) ensureMaps() {
	if s.st.Clusters == nil {
		s.st.Clusters = map[string]*cluster{}
	}

	for _, c := range s.st.Clusters {
		if c.Instances == nil {
			c.Instances = map[string]*instance{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "AlloyDB",
		Category:    "Databases",
		Description: "PostgreSQL-compatible clusters with primary/read-pool instances",
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

// RegisterRoutes registers the AlloyDB REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1beta/projects/{project}/locations/{location}/clusters"
	r.Handle("POST", base, s.createCluster)
	r.Handle("GET", base, s.listClusters)
	r.Handle("GET", base+"/{cluster}", s.getCluster)
	r.Handle("DELETE", base+"/{cluster}", s.deleteCluster)

	instBase := base + "/{cluster}/instances"
	r.Handle("POST", instBase, s.createInstance)
	r.Handle("GET", instBase, s.listInstances)
	r.Handle("GET", instBase+"/{instance}", s.getInstance)
	r.Handle("DELETE", instBase+"/{instance}", s.deleteInstance)
}

func (s *Service) clusterPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/clusters/"
}

func (s *Service) clusterName(r *http.Request) string {
	return s.clusterPrefix(r) + r.PathValue("cluster")
}

// ---- Clusters ----

func (s *Service) createCluster(w http.ResponseWriter, r *http.Request) {
	var body cluster
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.clusterPrefix(r) + body.Name
	body.State = "READY"
	body.Instances = map[string]*instance{}

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

// ---- Instances ----

func (s *Service) createInstance(w http.ResponseWriter, r *http.Request) {
	var body instance
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	if body.InstanceType == "" {
		body.InstanceType = "PRIMARY"
	}

	body.State = "READY"

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, exists := c.Instances[body.Name]; exists {
		httpx.AlreadyExists(w, "instance already exists: "+body.Name)

		return
	}

	c.Instances[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listInstances(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	names := make([]string, 0, len(c.Instances))
	for n := range c.Instances {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*instance, 0, len(names))
	for _, n := range names {
		items = append(items, c.Instances[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"instances": items})
}

func (s *Service) getInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	inst, ok := c.Instances[r.PathValue("instance")]
	if !ok {
		httpx.NotFound(w, "instance not found: "+r.PathValue("instance"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) deleteInstance(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, ok := c.Instances[r.PathValue("instance")]; !ok {
		httpx.NotFound(w, "instance not found: "+r.PathValue("instance"))

		return
	}

	delete(c.Instances, r.PathValue("instance"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
