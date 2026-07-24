// Package clouddeploy emulates Cloud Deploy (clouddeploy.googleapis.com/v1):
// delivery pipelines, their releases, and the rollouts each release creates.
package clouddeploy

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "clouddeploy"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type rollout struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type release struct {
	Name     string              `json:"name"`
	Rollouts map[string]*rollout `json:"rollouts"`
}

type pipeline struct {
	Name     string              `json:"name"`
	Releases map[string]*release `json:"releases"`
}

type state struct {
	Pipelines map[string]*pipeline `json:"pipelines"` // full path -> pipeline
}

// Service implements the Cloud Deploy emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Deploy store.
func New() *Service { return &Service{st: state{Pipelines: map[string]*pipeline{}}} }

func (s *Service) ensureMaps() {
	if s.st.Pipelines == nil {
		s.st.Pipelines = map[string]*pipeline{}
	}

	for _, p := range s.st.Pipelines {
		if p.Releases == nil {
			p.Releases = map[string]*release{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Deploy",
		Category:    "Developer Tools",
		Description: "Delivery pipelines, releases, and rollouts",
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

// RegisterRoutes registers the Cloud Deploy REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/deliveryPipelines"
	r.Handle("POST", base, s.createPipeline)
	r.Handle("GET", base, s.listPipelines)
	r.Handle("GET", base+"/{pipeline}", s.getPipeline)
	r.Handle("DELETE", base+"/{pipeline}", s.deletePipeline)

	relBase := base + "/{pipeline}/releases"
	r.Handle("POST", relBase, s.createRelease)
	r.Handle("GET", relBase, s.listReleases)
	r.Handle("GET", relBase+"/{release}", s.getRelease)

	rolloutBase := relBase + "/{release}/rollouts"
	r.Handle("POST", rolloutBase, s.createRollout)
	r.Handle("GET", rolloutBase, s.listRollouts)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/deliveryPipelines/"
}

func (s *Service) pipelineName(r *http.Request) string {
	return s.prefix(r) + r.PathValue("pipeline")
}

// ---- Pipelines ----

func (s *Service) createPipeline(w http.ResponseWriter, r *http.Request) {
	var body pipeline
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r) + body.Name
	body.Releases = map[string]*release{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Pipelines[name]; exists {
		httpx.AlreadyExists(w, "pipeline already exists: "+name)

		return
	}

	s.st.Pipelines[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listPipelines(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Pipelines {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*pipeline, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Pipelines[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deliveryPipelines": items})
}

func (s *Service) getPipeline(w http.ResponseWriter, r *http.Request) {
	name := s.pipelineName(r)

	s.mu.RLock()
	p, ok := s.st.Pipelines[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "pipeline not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) deletePipeline(w http.ResponseWriter, r *http.Request) {
	name := s.pipelineName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Pipelines[name]; !ok {
		httpx.NotFound(w, "pipeline not found: "+name)

		return
	}

	delete(s.st.Pipelines, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Releases ----

func (s *Service) createRelease(w http.ResponseWriter, r *http.Request) {
	var body release
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.st.Pipelines[s.pipelineName(r)]
	if !ok {
		httpx.NotFound(w, "pipeline not found: "+s.pipelineName(r))

		return
	}

	if _, exists := p.Releases[body.Name]; exists {
		httpx.AlreadyExists(w, "release already exists: "+body.Name)

		return
	}

	body.Rollouts = map[string]*rollout{}
	p.Releases[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listReleases(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.st.Pipelines[s.pipelineName(r)]
	if !ok {
		httpx.NotFound(w, "pipeline not found: "+s.pipelineName(r))

		return
	}

	names := make([]string, 0, len(p.Releases))
	for n := range p.Releases {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*release, 0, len(names))
	for _, n := range names {
		items = append(items, p.Releases[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"releases": items})
}

func (s *Service) getRelease(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.st.Pipelines[s.pipelineName(r)]
	if !ok {
		httpx.NotFound(w, "pipeline not found: "+s.pipelineName(r))

		return
	}

	rel, ok := p.Releases[r.PathValue("release")]
	if !ok {
		httpx.NotFound(w, "release not found: "+r.PathValue("release"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, rel)
}

// ---- Rollouts ----

func (s *Service) createRollout(w http.ResponseWriter, r *http.Request) {
	var body rollout
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.st.Pipelines[s.pipelineName(r)]
	if !ok {
		httpx.NotFound(w, "pipeline not found: "+s.pipelineName(r))

		return
	}

	rel, ok := p.Releases[r.PathValue("release")]
	if !ok {
		httpx.NotFound(w, "release not found: "+r.PathValue("release"))

		return
	}

	if _, exists := rel.Rollouts[body.Name]; exists {
		httpx.AlreadyExists(w, "rollout already exists: "+body.Name)

		return
	}

	body.State = "SUCCEEDED"
	rel.Rollouts[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listRollouts(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p, ok := s.st.Pipelines[s.pipelineName(r)]
	if !ok {
		httpx.NotFound(w, "pipeline not found: "+s.pipelineName(r))

		return
	}

	rel, ok := p.Releases[r.PathValue("release")]
	if !ok {
		httpx.NotFound(w, "release not found: "+r.PathValue("release"))

		return
	}

	names := make([]string, 0, len(rel.Rollouts))
	for n := range rel.Rollouts {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*rollout, 0, len(names))
	for _, n := range names {
		items = append(items, rel.Rollouts[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rollouts": items})
}
