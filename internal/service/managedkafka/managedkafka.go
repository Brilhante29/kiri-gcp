// Package managedkafka emulates Managed Service for Apache Kafka
// (managedkafka.googleapis.com/v1): clusters and their topics.
//
// Routes are prefixed with "/managedkafka" — real GCP would serve this at
// the same "/v1/projects/{p}/locations/{l}/clusters" path "gke" already owns
// here, disambiguated in production only by API host
// (managedkafka.googleapis.com vs container.googleapis.com), which this
// emulator's flat, host-less router cannot do. Unlike the gkeautopilot/
// cloudrunjobs/alloydb fixes elsewhere in this codebase, no real alternate
// API version is known to exist for Managed Kafka to borrow instead, so this
// prefix is an emulator-only convention: a real Managed Kafka client SDK
// pointed at this emulator will not reach these routes without a manual
// path override. Reachable and testable via direct REST regardless.
package managedkafka

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "managedkafka"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type topic struct {
	Name              string `json:"name"`
	PartitionCount    int    `json:"partitionCount,omitempty"`
	ReplicationFactor int    `json:"replicationFactor,omitempty"`
}

type cluster struct {
	Name   string            `json:"name"`
	State  string            `json:"state"`
	Topics map[string]*topic `json:"topics"`
}

type state struct {
	Clusters map[string]*cluster `json:"clusters"` // full path -> cluster
}

// Service implements the Managed Kafka emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Managed Kafka store.
func New() *Service { return &Service{st: state{Clusters: map[string]*cluster{}}} }

func (s *Service) ensureMaps() {
	if s.st.Clusters == nil {
		s.st.Clusters = map[string]*cluster{}
	}

	for _, c := range s.st.Clusters {
		if c.Topics == nil {
			c.Topics = map[string]*topic{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Managed Service for Apache Kafka",
		Category:    "Messaging & Integration",
		Description: "Fully managed Kafka clusters and topics",
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

// RegisterRoutes registers the Managed Kafka REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/managedkafka/v1/projects/{project}/locations/{location}/clusters"
	r.Handle("POST", base, s.createCluster)
	r.Handle("GET", base, s.listClusters)
	r.Handle("GET", base+"/{cluster}", s.getCluster)
	r.Handle("DELETE", base+"/{cluster}", s.deleteCluster)

	topicBase := base + "/{cluster}/topics"
	r.Handle("POST", topicBase, s.createTopic)
	r.Handle("GET", topicBase, s.listTopics)
	r.Handle("GET", topicBase+"/{topic}", s.getTopic)
	r.Handle("DELETE", topicBase+"/{topic}", s.deleteTopic)
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
	body.State = "ACTIVE"
	body.Topics = map[string]*topic{}

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

// ---- Topics ----

func (s *Service) createTopic(w http.ResponseWriter, r *http.Request) {
	var body topic
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	if body.PartitionCount == 0 {
		body.PartitionCount = 1
	}

	if body.ReplicationFactor == 0 {
		body.ReplicationFactor = 3
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, exists := c.Topics[body.Name]; exists {
		httpx.AlreadyExists(w, "topic already exists: "+body.Name)

		return
	}

	c.Topics[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listTopics(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	names := make([]string, 0, len(c.Topics))
	for n := range c.Topics {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*topic, 0, len(names))
	for _, n := range names {
		items = append(items, c.Topics[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"topics": items})
}

func (s *Service) getTopic(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	t, ok := c.Topics[r.PathValue("topic")]
	if !ok {
		httpx.NotFound(w, "topic not found: "+r.PathValue("topic"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTopic(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, ok := s.st.Clusters[s.clusterName(r)]
	if !ok {
		httpx.NotFound(w, "cluster not found: "+s.clusterName(r))

		return
	}

	if _, ok := c.Topics[r.PathValue("topic")]; !ok {
		httpx.NotFound(w, "topic not found: "+r.PathValue("topic"))

		return
	}

	delete(c.Topics, r.PathValue("topic"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
