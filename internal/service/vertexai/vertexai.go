// Package vertexai emulates Vertex AI (aiplatform.googleapis.com/v1):
// custom training jobs, models, and endpoints with deploy/predict.
package vertexai

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "vertexai"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type customJob struct {
	Name        string         `json:"name"`
	DisplayName string         `json:"displayName,omitempty"`
	JobSpec     map[string]any `json:"jobSpec,omitempty"`
	State       string         `json:"state"`
}

type model struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
}

type deployedModel struct {
	ID    string `json:"id"`
	Model string `json:"model"`
}

type endpoint struct {
	Name           string                    `json:"name"`
	DisplayName    string                    `json:"displayName,omitempty"`
	DeployedModels map[string]*deployedModel `json:"deployedModels"`
}

type state struct {
	CustomJobs map[string]*customJob `json:"customJobs"`
	Models     map[string]*model     `json:"models"`
	Endpoints  map[string]*endpoint  `json:"endpoints"`
}

// Service implements the Vertex AI emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Vertex AI store.
func New() *Service {
	return &Service{st: state{
		CustomJobs: map[string]*customJob{},
		Models:     map[string]*model{},
		Endpoints:  map[string]*endpoint{},
	}}
}

func (s *Service) ensureMaps() {
	if s.st.CustomJobs == nil {
		s.st.CustomJobs = map[string]*customJob{}
	}

	if s.st.Models == nil {
		s.st.Models = map[string]*model{}
	}

	if s.st.Endpoints == nil {
		s.st.Endpoints = map[string]*endpoint{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Vertex AI",
		Category:    "Analytics & ML",
		Description: "Custom training jobs, models, and endpoint deploy/predict",
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

// RegisterRoutes registers the Vertex AI REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}"

	jobBase := base + "/customJobs"
	r.Handle("POST", jobBase, s.createCustomJob)
	r.Handle("GET", jobBase, s.listCustomJobs)
	r.Handle("GET", jobBase+"/{job}", s.getCustomJob)
	// {job} also matches "jobId:cancel".
	r.Handle("POST", jobBase+"/{job}", s.jobAction)

	modelBase := base + "/models"
	r.Handle("POST", modelBase, s.createModel)
	r.Handle("GET", modelBase, s.listModels)
	r.Handle("GET", modelBase+"/{model}", s.getModel)
	r.Handle("DELETE", modelBase+"/{model}", s.deleteModel)

	epBase := base + "/endpoints"
	r.Handle("POST", epBase, s.createEndpoint)
	r.Handle("GET", epBase, s.listEndpoints)
	r.Handle("GET", epBase+"/{endpoint}", s.getEndpoint)
	r.Handle("DELETE", epBase+"/{endpoint}", s.deleteEndpoint)
	// {endpoint} also matches "id:deployModel" / "id:predict".
	r.Handle("POST", epBase+"/{endpoint}", s.endpointAction)
}

func (s *Service) prefix(r *http.Request, kind string) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/" + kind + "/"
}

// ---- Custom jobs ----

func (s *Service) createCustomJob(w http.ResponseWriter, r *http.Request) {
	var body customJob
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	name := s.prefix(r, "customJobs") + httpx.ID(8)
	body.Name = name
	body.State = "JOB_STATE_RUNNING"

	s.mu.Lock()
	s.st.CustomJobs[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listCustomJobs(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "customJobs")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.CustomJobs {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*customJob, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.CustomJobs[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"customJobs": items})
}

func (s *Service) getCustomJob(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "customJobs") + r.PathValue("job")

	s.mu.RLock()
	j, ok := s.st.CustomJobs[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "custom job not found: "+name)

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

	name := s.prefix(r, "customJobs") + id

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.st.CustomJobs[name]
	if !ok {
		httpx.NotFound(w, "custom job not found: "+name)

		return
	}

	j.State = "JOB_STATE_CANCELLED"

	httpx.WriteJSON(w, http.StatusOK, j)
}

// ---- Models ----

func (s *Service) createModel(w http.ResponseWriter, r *http.Request) {
	var body model
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	name := s.prefix(r, "models") + httpx.ID(8)
	body.Name = name

	s.mu.Lock()
	s.st.Models[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listModels(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "models")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Models {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*model, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Models[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"models": items})
}

func (s *Service) getModel(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "models") + r.PathValue("model")

	s.mu.RLock()
	m, ok := s.st.Models[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "model not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, m)
}

func (s *Service) deleteModel(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "models") + r.PathValue("model")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Models[name]; !ok {
		httpx.NotFound(w, "model not found: "+name)

		return
	}

	delete(s.st.Models, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Endpoints ----

func (s *Service) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var body endpoint
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	name := s.prefix(r, "endpoints") + httpx.ID(8)
	body.Name = name
	body.DeployedModels = map[string]*deployedModel{}

	s.mu.Lock()
	s.st.Endpoints[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listEndpoints(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "endpoints")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Endpoints {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*endpoint, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Endpoints[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"endpoints": items})
}

func (s *Service) getEndpoint(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "endpoints") + r.PathValue("endpoint")

	s.mu.RLock()
	e, ok := s.st.Endpoints[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, e)
}

func (s *Service) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "endpoints") + r.PathValue("endpoint")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Endpoints[name]; !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	delete(s.st.Endpoints, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) endpointAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("endpoint"))
	name := s.prefix(r, "endpoints") + id

	switch verb {
	case "deployModel":
		s.deployModel(w, r, name)
	case "predict":
		s.predict(w, name)
	default:
		httpx.NotFound(w, "unknown method: "+verb)
	}
}

func (s *Service) deployModel(w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		DeployedModel deployedModel `json:"deployedModel"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.st.Endpoints[name]
	if !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	dm := body.DeployedModel
	dm.ID = httpx.ID(8)
	e.DeployedModels[dm.ID] = &dm

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deployedModel": dm})
}

// predict returns a stub prediction envelope: real inference is out of
// scope for an emulator, but the response shape and deployed-model
// existence check are real.
func (s *Service) predict(w http.ResponseWriter, name string) {
	s.mu.RLock()
	e, ok := s.st.Endpoints[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	if len(e.DeployedModels) == 0 {
		httpx.BadRequest(w, "endpoint has no deployed model")

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"predictions": []any{map[string]any{}}})
}
