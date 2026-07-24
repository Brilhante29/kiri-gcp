// Package apigateway emulates API Gateway (apigateway.googleapis.com/v1):
// APIs, their configs, and the gateways that serve a config.
package apigateway

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "apigateway"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type apiConfig struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type api struct {
	Name    string                `json:"name"`
	State   string                `json:"state"`
	Configs map[string]*apiConfig `json:"configs"`
}

type gateway struct {
	Name            string `json:"name"`
	APIConfig       string `json:"apiConfig"`
	State           string `json:"state"`
	DefaultHostname string `json:"defaultHostname"`
}

type state struct {
	APIs     map[string]*api     `json:"apis"`     // projects/{p}/locations/global/apis/{a} -> api
	Gateways map[string]*gateway `json:"gateways"` // full path -> gateway
}

// Service implements the API Gateway emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty API Gateway store.
func New() *Service {
	return &Service{st: state{APIs: map[string]*api{}, Gateways: map[string]*gateway{}}}
}

func (s *Service) ensureMaps() {
	if s.st.APIs == nil {
		s.st.APIs = map[string]*api{}
	}

	if s.st.Gateways == nil {
		s.st.Gateways = map[string]*gateway{}
	}

	for _, a := range s.st.APIs {
		if a.Configs == nil {
			a.Configs = map[string]*apiConfig{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "API Gateway",
		Category:    "Networking",
		Description: "Managed gateway for backend APIs: apis, configs, gateways",
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

// RegisterRoutes registers the API Gateway REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	apiBase := "/v1/projects/{project}/locations/global/apis"
	r.Handle("POST", apiBase, s.createAPI)
	r.Handle("GET", apiBase, s.listAPIs)
	r.Handle("GET", apiBase+"/{api}", s.getAPI)
	r.Handle("DELETE", apiBase+"/{api}", s.deleteAPI)

	cfgBase := apiBase + "/{api}/configs"
	r.Handle("POST", cfgBase, s.createConfig)
	r.Handle("GET", cfgBase, s.listConfigs)
	r.Handle("GET", cfgBase+"/{config}", s.getConfig)

	gwBase := "/v1/projects/{project}/locations/{location}/gateways"
	r.Handle("POST", gwBase, s.createGateway)
	r.Handle("GET", gwBase, s.listGateways)
	r.Handle("GET", gwBase+"/{gateway}", s.getGateway)
	r.Handle("DELETE", gwBase+"/{gateway}", s.deleteGateway)
}

func (s *Service) apiPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/global/apis/"
}

func (s *Service) gwPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/gateways/"
}

// ---- APIs ----

func (s *Service) createAPI(w http.ResponseWriter, r *http.Request) {
	var body api
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.apiPrefix(r) + body.Name
	body.State = "ACTIVE"
	body.Configs = map[string]*apiConfig{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.APIs[name]; exists {
		httpx.AlreadyExists(w, "api already exists: "+name)

		return
	}

	s.st.APIs[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listAPIs(w http.ResponseWriter, r *http.Request) {
	prefix := s.apiPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.APIs {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*api, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.APIs[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"apis": items})
}

func (s *Service) getAPI(w http.ResponseWriter, r *http.Request) {
	name := s.apiPrefix(r) + r.PathValue("api")

	s.mu.RLock()
	a, ok := s.st.APIs[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "api not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) deleteAPI(w http.ResponseWriter, r *http.Request) {
	name := s.apiPrefix(r) + r.PathValue("api")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.APIs[name]; !ok {
		httpx.NotFound(w, "api not found: "+name)

		return
	}

	delete(s.st.APIs, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- API configs ----

func (s *Service) createConfig(w http.ResponseWriter, r *http.Request) {
	var body apiConfig
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

	a, ok := s.st.APIs[s.apiPrefix(r)+r.PathValue("api")]
	if !ok {
		httpx.NotFound(w, "api not found")

		return
	}

	if _, exists := a.Configs[body.Name]; exists {
		httpx.AlreadyExists(w, "api config already exists: "+body.Name)

		return
	}

	body.State = "ACTIVE"
	a.Configs[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listConfigs(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a, ok := s.st.APIs[s.apiPrefix(r)+r.PathValue("api")]
	if !ok {
		httpx.NotFound(w, "api not found")

		return
	}

	names := make([]string, 0, len(a.Configs))
	for n := range a.Configs {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*apiConfig, 0, len(names))
	for _, n := range names {
		items = append(items, a.Configs[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"apiConfigs": items})
}

func (s *Service) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	a, ok := s.st.APIs[s.apiPrefix(r)+r.PathValue("api")]
	if !ok {
		httpx.NotFound(w, "api not found")

		return
	}

	c, ok := a.Configs[r.PathValue("config")]
	if !ok {
		httpx.NotFound(w, "api config not found: "+r.PathValue("config"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, c)
}

// ---- Gateways ----

func (s *Service) createGateway(w http.ResponseWriter, r *http.Request) {
	var body gateway
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" || body.APIConfig == "" {
		httpx.BadRequest(w, "name and apiConfig are required")

		return
	}

	name := s.gwPrefix(r) + body.Name
	body.State = "ACTIVE"
	body.DefaultHostname = body.Name + "-" + httpx.ID(4) + ".gateway.dev"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Gateways[name]; exists {
		httpx.AlreadyExists(w, "gateway already exists: "+name)

		return
	}

	s.st.Gateways[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listGateways(w http.ResponseWriter, r *http.Request) {
	prefix := s.gwPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Gateways {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*gateway, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Gateways[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"gateways": items})
}

func (s *Service) getGateway(w http.ResponseWriter, r *http.Request) {
	name := s.gwPrefix(r) + r.PathValue("gateway")

	s.mu.RLock()
	g, ok := s.st.Gateways[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "gateway not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, g)
}

func (s *Service) deleteGateway(w http.ResponseWriter, r *http.Request) {
	name := s.gwPrefix(r) + r.PathValue("gateway")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Gateways[name]; !ok {
		httpx.NotFound(w, "gateway not found: "+name)

		return
	}

	delete(s.st.Gateways, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
