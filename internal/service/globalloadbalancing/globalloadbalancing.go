// Package globalloadbalancing emulates global Cloud Load Balancing resources
// (compute.googleapis.com/v1): URL maps, target HTTP proxies, and global
// forwarding rules — the chain a global external HTTP(S) load balancer wires
// together.
package globalloadbalancing

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "globalloadbalancing"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type urlMap struct {
	Name           string `json:"name"`
	DefaultService string `json:"defaultService,omitempty"`
}

type targetHTTPProxy struct {
	Name   string `json:"name"`
	URLMap string `json:"urlMap,omitempty"`
}

type forwardingRule struct {
	Name      string `json:"name"`
	Target    string `json:"target,omitempty"`
	IPAddress string `json:"IPAddress"`
	PortRange string `json:"portRange,omitempty"`
}

type state struct {
	URLMaps           map[string]*urlMap          `json:"urlMaps"`
	TargetHTTPProxies map[string]*targetHTTPProxy `json:"targetHttpProxies"`
	ForwardingRules   map[string]*forwardingRule  `json:"forwardingRules"`
}

// Service implements the global load balancing emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty global load balancing store.
func New() *Service {
	return &Service{st: state{
		URLMaps:           map[string]*urlMap{},
		TargetHTTPProxies: map[string]*targetHTTPProxy{},
		ForwardingRules:   map[string]*forwardingRule{},
	}}
}

func (s *Service) ensureMaps() {
	if s.st.URLMaps == nil {
		s.st.URLMaps = map[string]*urlMap{}
	}

	if s.st.TargetHTTPProxies == nil {
		s.st.TargetHTTPProxies = map[string]*targetHTTPProxy{}
	}

	if s.st.ForwardingRules == nil {
		s.st.ForwardingRules = map[string]*forwardingRule{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Global Load Balancing",
		Category:    "Networking",
		Description: "URL maps, target HTTP proxies, and global forwarding rules",
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

// RegisterRoutes registers the global load balancing REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	umBase := "/compute/v1/projects/{project}/global/urlMaps"
	r.Handle("POST", umBase, s.createURLMap)
	r.Handle("GET", umBase, s.listURLMaps)
	r.Handle("GET", umBase+"/{name}", s.getURLMap)
	r.Handle("DELETE", umBase+"/{name}", s.deleteURLMap)

	proxyBase := "/compute/v1/projects/{project}/global/targetHttpProxies"
	r.Handle("POST", proxyBase, s.createProxy)
	r.Handle("GET", proxyBase, s.listProxies)
	r.Handle("GET", proxyBase+"/{name}", s.getProxy)
	r.Handle("DELETE", proxyBase+"/{name}", s.deleteProxy)

	frBase := "/compute/v1/projects/{project}/global/forwardingRules"
	r.Handle("POST", frBase, s.createForwardingRule)
	r.Handle("GET", frBase, s.listForwardingRules)
	r.Handle("GET", frBase+"/{name}", s.getForwardingRule)
	r.Handle("DELETE", frBase+"/{name}", s.deleteForwardingRule)
}

func (s *Service) prefix(r *http.Request, kind string) string {
	return "projects/" + r.PathValue("project") + "/global/" + kind + "/"
}

// ---- URL maps ----

func (s *Service) createURLMap(w http.ResponseWriter, r *http.Request) {
	var body urlMap
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r, "urlMaps") + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.URLMaps[name]; exists {
		httpx.AlreadyExists(w, "URL map already exists: "+name)

		return
	}

	s.st.URLMaps[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listURLMaps(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "urlMaps")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.URLMaps {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*urlMap, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.URLMaps[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getURLMap(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "urlMaps") + r.PathValue("name")

	s.mu.RLock()
	um, ok := s.st.URLMaps[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "URL map not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, um)
}

func (s *Service) deleteURLMap(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "urlMaps") + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.URLMaps[name]; !ok {
		httpx.NotFound(w, "URL map not found: "+name)

		return
	}

	delete(s.st.URLMaps, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Target HTTP proxies ----

func (s *Service) createProxy(w http.ResponseWriter, r *http.Request) {
	var body targetHTTPProxy
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r, "targetHttpProxies") + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.TargetHTTPProxies[name]; exists {
		httpx.AlreadyExists(w, "target proxy already exists: "+name)

		return
	}

	s.st.TargetHTTPProxies[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listProxies(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "targetHttpProxies")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.TargetHTTPProxies {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*targetHTTPProxy, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.TargetHTTPProxies[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getProxy(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "targetHttpProxies") + r.PathValue("name")

	s.mu.RLock()
	p, ok := s.st.TargetHTTPProxies[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "target proxy not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) deleteProxy(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "targetHttpProxies") + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.TargetHTTPProxies[name]; !ok {
		httpx.NotFound(w, "target proxy not found: "+name)

		return
	}

	delete(s.st.TargetHTTPProxies, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Global forwarding rules ----

func (s *Service) createForwardingRule(w http.ResponseWriter, r *http.Request) {
	var body forwardingRule
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.prefix(r, "forwardingRules") + body.Name
	if body.IPAddress == "" {
		body.IPAddress = "34.120." + httpx.NumericID()[:2] + "." + httpx.NumericID()[:3]
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.ForwardingRules[name]; exists {
		httpx.AlreadyExists(w, "forwarding rule already exists: "+name)

		return
	}

	s.st.ForwardingRules[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listForwardingRules(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "forwardingRules")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.ForwardingRules {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*forwardingRule, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.ForwardingRules[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getForwardingRule(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "forwardingRules") + r.PathValue("name")

	s.mu.RLock()
	fr, ok := s.st.ForwardingRules[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "forwarding rule not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, fr)
}

func (s *Service) deleteForwardingRule(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "forwardingRules") + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.ForwardingRules[name]; !ok {
		httpx.NotFound(w, "forwarding rule not found: "+name)

		return
	}

	delete(s.st.ForwardingRules, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
