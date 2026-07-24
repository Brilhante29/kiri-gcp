// Package cloudbuild emulates Cloud Build (cloudbuild.googleapis.com/v1):
// build triggers and the builds they (or a direct submission) produce.
package cloudbuild

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "cloudbuild"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type build struct {
	ID     string           `json:"id"`
	Status string           `json:"status"`
	Steps  []map[string]any `json:"steps,omitempty"`
	Images []string         `json:"images,omitempty"`
}

type trigger struct {
	ID          string         `json:"id"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Build       map[string]any `json:"build,omitempty"`
}

type state struct {
	Builds   map[string]*build   `json:"builds"`   // "project:id" -> build
	Triggers map[string]*trigger `json:"triggers"` // "project:id" -> trigger
}

// Service implements the Cloud Build emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Build store.
func New() *Service {
	return &Service{st: state{Builds: map[string]*build{}, Triggers: map[string]*trigger{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Builds == nil {
		s.st.Builds = map[string]*build{}
	}

	if s.st.Triggers == nil {
		s.st.Triggers = map[string]*trigger{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Build",
		Category:    "Developer Tools",
		Description: "Build triggers and the builds they produce",
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

// RegisterRoutes registers the Cloud Build REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	buildBase := "/v1/projects/{project}/builds"
	r.Handle("POST", buildBase, s.createBuild)
	r.Handle("GET", buildBase, s.listBuilds)
	r.Handle("GET", buildBase+"/{build}", s.getBuild)

	trigBase := "/v1/projects/{project}/triggers"
	r.Handle("POST", trigBase, s.createTrigger)
	r.Handle("GET", trigBase, s.listTriggers)
	r.Handle("GET", trigBase+"/{trigger}", s.getTrigger)
	r.Handle("DELETE", trigBase+"/{trigger}", s.deleteTrigger)
	// {trigger} also matches "id:run".
	r.Handle("POST", trigBase+"/{trigger}", s.runTrigger)
}

func (s *Service) buildPrefix(project string) string { return project + ":" }

// ---- Builds ----

func (s *Service) createBuild(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body struct {
		Steps  []map[string]any `json:"steps"`
		Images []string         `json:"images"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	id := httpx.ID(8)
	b := &build{ID: id, Status: "SUCCESS", Steps: body.Steps, Images: body.Images}

	s.mu.Lock()
	s.st.Builds[s.buildPrefix(project)+id] = b
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) listBuilds(w http.ResponseWriter, r *http.Request) {
	prefix := s.buildPrefix(r.PathValue("project"))

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)
	for k := range s.st.Builds {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]*build, 0, len(keys))
	for _, k := range keys {
		items = append(items, s.st.Builds[k])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"builds": items})
}

func (s *Service) getBuild(w http.ResponseWriter, r *http.Request) {
	key := s.buildPrefix(r.PathValue("project")) + r.PathValue("build")

	s.mu.RLock()
	b, ok := s.st.Builds[key]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "build not found: "+r.PathValue("build"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, b)
}

// ---- Triggers ----

func (s *Service) createTrigger(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body trigger
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	body.ID = httpx.ID(8)

	s.mu.Lock()
	s.st.Triggers[s.buildPrefix(project)+body.ID] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listTriggers(w http.ResponseWriter, r *http.Request) {
	prefix := s.buildPrefix(r.PathValue("project"))

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)
	for k := range s.st.Triggers {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]*trigger, 0, len(keys))
	for _, k := range keys {
		items = append(items, s.st.Triggers[k])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"triggers": items})
}

func (s *Service) getTrigger(w http.ResponseWriter, r *http.Request) {
	key := s.buildPrefix(r.PathValue("project")) + r.PathValue("trigger")

	s.mu.RLock()
	t, ok := s.st.Triggers[key]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "trigger not found: "+r.PathValue("trigger"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	key := s.buildPrefix(r.PathValue("project")) + r.PathValue("trigger")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Triggers[key]; !ok {
		httpx.NotFound(w, "trigger not found: "+r.PathValue("trigger"))

		return
	}

	delete(s.st.Triggers, key)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) runTrigger(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	id, verb := httpx.SplitVerb(r.PathValue("trigger"))

	if verb != "run" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Triggers[s.buildPrefix(project)+id]; !ok {
		httpx.NotFound(w, "trigger not found: "+id)

		return
	}

	buildID := httpx.ID(8)
	b := &build{ID: buildID, Status: "SUCCESS"}
	s.st.Builds[s.buildPrefix(project)+buildID] = b

	httpx.WriteJSON(w, http.StatusOK, b)
}
