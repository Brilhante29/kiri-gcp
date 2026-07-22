// Package cloudscheduler emulates Cloud Scheduler
// (cloudscheduler.googleapis.com/v1): cron jobs with pause/resume/run
// lifecycle actions. When a job targets a Pub/Sub topic, running it (via
// :run, or in principle its own schedule) publishes through PublishFunc,
// wired to the real Pub/Sub service at server startup.
package cloudscheduler

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "cloudscheduler"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

// PublishFunc delivers a message to a Pub/Sub topic. Wired by the server to
// the real Pub/Sub service; nil (a no-op) in isolated tests.
var PublishFunc func(topicPath, data string, attrs map[string]string) []string

type pubsubTarget struct {
	TopicName string `json:"topicName,omitempty"`
	Data      string `json:"data,omitempty"`
}

type job struct {
	Name         string        `json:"name"`
	Schedule     string        `json:"schedule,omitempty"`
	State        string        `json:"state"`
	PubsubTarget *pubsubTarget `json:"pubsubTarget,omitempty"`
}

type state struct {
	Jobs map[string]*job `json:"jobs"` // full path -> job
}

// Service implements the Cloud Scheduler emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Scheduler store.
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
		Display:     "Cloud Scheduler",
		Category:    "Application Integration",
		Description: "Managed cron jobs with pause/resume/run and Pub/Sub targets",
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

// RegisterRoutes registers the Cloud Scheduler REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/jobs"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	// {job} also matches "name:pause" / "name:resume" / "name:run".
	r.Handle("GET", base+"/{job}", s.get)
	r.Handle("POST", base+"/{job}", s.jobAction)
	r.Handle("DELETE", base+"/{job}", s.delete)
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
		body.Name = s.prefix(r) + "job-" + httpx.ID(4)
	}

	body.State = "ENABLED"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Jobs[body.Name]; exists {
		httpx.AlreadyExists(w, "job already exists: "+body.Name)

		return
	}

	s.st.Jobs[body.Name] = &body

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

func (s *Service) jobAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("job"))
	name := s.prefix(r) + id

	s.mu.Lock()
	j, ok := s.st.Jobs[name]
	if !ok {
		s.mu.Unlock()
		httpx.NotFound(w, "job not found: "+name)

		return
	}

	switch verb {
	case "pause":
		j.State = "PAUSED"
	case "resume":
		j.State = "ENABLED"
	case "run":
		if j.PubsubTarget != nil && PublishFunc != nil {
			PublishFunc(j.PubsubTarget.TopicName, j.PubsubTarget.Data, nil)
		}
	default:
		s.mu.Unlock()
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}
	s.mu.Unlock()

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
