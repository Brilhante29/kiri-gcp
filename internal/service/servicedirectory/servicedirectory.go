// Package servicedirectory emulates Service Directory
// (servicedirectory.googleapis.com/v1): a three-level hierarchy of
// namespaces, services, and endpoints, plus the :resolve custom method
// clients use to look up a service's live endpoints.
package servicedirectory

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "servicedirectory"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type endpoint struct {
	Name     string            `json:"name"` // .../services/{s}/endpoints/{e}
	Address  string            `json:"address"`
	Port     int               `json:"port"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type svcEntry struct {
	Name      string            `json:"name"` // .../namespaces/{n}/services/{s}
	Metadata  map[string]string `json:"metadata,omitempty"`
	Endpoints map[string]*endpoint `json:"endpoints"` // endpoint name -> endpoint
}

type namespace struct {
	Name     string              `json:"name"` // projects/{p}/locations/{l}/namespaces/{n}
	Labels   map[string]string   `json:"labels,omitempty"`
	Services map[string]*svcEntry `json:"services"` // service name -> service
}

type state struct {
	Namespaces map[string]*namespace `json:"namespaces"` // namespace name -> namespace
}

// Service implements the Service Directory emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Service Directory store.
func New() *Service { return &Service{st: state{Namespaces: map[string]*namespace{}}} }

func (s *Service) ensureMaps() {
	if s.st.Namespaces == nil {
		s.st.Namespaces = map[string]*namespace{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Service Directory",
		Category:    "Networking",
		Description: "Managed service discovery: namespaces, services, endpoints, resolve",
		Fidelity:    service.FidelityA,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the Service Directory REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/namespaces"

	r.Handle("POST", base, s.createNamespace)
	r.Handle("GET", base, s.listNamespaces)
	r.Handle("GET", base+"/{namespace}", s.getNamespace)
	r.Handle("DELETE", base+"/{namespace}", s.deleteNamespace)

	svcBase := base + "/{namespace}/services"
	r.Handle("POST", svcBase, s.createService)
	r.Handle("GET", svcBase, s.listServices)
	// {service} also matches "name:resolve" custom-method segments.
	r.Handle("GET", svcBase+"/{service}", s.getOrResolveService)
	r.Handle("POST", svcBase+"/{service}", s.resolveService)
	r.Handle("DELETE", svcBase+"/{service}", s.deleteService)

	epBase := svcBase + "/{service}/endpoints"
	r.Handle("POST", epBase, s.createEndpoint)
	r.Handle("GET", epBase, s.listEndpoints)
	r.Handle("GET", epBase+"/{endpoint}", s.getEndpoint)
	r.Handle("DELETE", epBase+"/{endpoint}", s.deleteEndpoint)
}

// ---- Namespaces ----

func (s *Service) createNamespace(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("namespaceId")

	var body namespace
	_ = httpx.DecodeJSON(r, &body)

	if id == "" {
		httpx.BadRequest(w, "namespaceId query parameter is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/namespaces/" + id

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Namespaces[name]; exists {
		httpx.AlreadyExists(w, "namespace already exists: "+name)

		return
	}

	ns := &namespace{Name: name, Labels: body.Labels, Services: map[string]*svcEntry{}}
	s.st.Namespaces[name] = ns

	httpx.WriteJSON(w, http.StatusOK, ns)
}

func (s *Service) listNamespaces(w http.ResponseWriter, r *http.Request) {
	prefix := "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/namespaces/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Namespaces {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*namespace, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Namespaces[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"namespaces": items})
}

func (s *Service) namespaceName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/namespaces/" + r.PathValue("namespace")
}

func (s *Service) getNamespace(w http.ResponseWriter, r *http.Request) {
	name := s.namespaceName(r)

	s.mu.RLock()
	ns, ok := s.st.Namespaces[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "namespace not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, ns)
}

func (s *Service) deleteNamespace(w http.ResponseWriter, r *http.Request) {
	name := s.namespaceName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Namespaces[name]; !ok {
		httpx.NotFound(w, "namespace not found: "+name)

		return
	}

	delete(s.st.Namespaces, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Services ----

func (s *Service) createService(w http.ResponseWriter, r *http.Request) {
	nsName := s.namespaceName(r)
	id := r.URL.Query().Get("serviceId")

	var body svcEntry
	_ = httpx.DecodeJSON(r, &body)

	if id == "" {
		httpx.BadRequest(w, "serviceId query parameter is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ns, ok := s.st.Namespaces[nsName]
	if !ok {
		httpx.NotFound(w, "namespace not found: "+nsName)

		return
	}

	name := nsName + "/services/" + id
	if _, exists := ns.Services[name]; exists {
		httpx.AlreadyExists(w, "service already exists: "+name)

		return
	}

	svc := &svcEntry{Name: name, Metadata: body.Metadata, Endpoints: map[string]*endpoint{}}
	ns.Services[name] = svc

	httpx.WriteJSON(w, http.StatusOK, svc)
}

func (s *Service) listServices(w http.ResponseWriter, r *http.Request) {
	nsName := s.namespaceName(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	ns, ok := s.st.Namespaces[nsName]
	if !ok {
		httpx.NotFound(w, "namespace not found: "+nsName)

		return
	}

	names := make([]string, 0, len(ns.Services))
	for n := range ns.Services {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*svcEntry, 0, len(names))
	for _, n := range names {
		items = append(items, ns.Services[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *Service) getOrResolveService(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("service"))
	name := s.namespaceName(r) + "/services/" + id

	s.mu.RLock()
	svc, ok := s.lookupService(name, r)
	s.mu.RUnlock()

	if verb == "resolve" {
		s.writeResolve(w, svc, ok, name)

		return
	}

	if !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, svc)
}

func (s *Service) resolveService(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("service"))
	if verb != "resolve" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	name := s.namespaceName(r) + "/services/" + id

	s.mu.RLock()
	svc, ok := s.lookupService(name, r)
	s.mu.RUnlock()

	s.writeResolve(w, svc, ok, name)
}

func (s *Service) writeResolve(w http.ResponseWriter, svc *svcEntry, ok bool, name string) {
	if !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	endpoints := make([]*endpoint, 0, len(svc.Endpoints))
	for _, e := range svc.Endpoints {
		endpoints = append(endpoints, e)
	}

	sort.Slice(endpoints, func(i, j int) bool { return endpoints[i].Name < endpoints[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"service": map[string]any{
			"name":      svc.Name,
			"metadata":  svc.Metadata,
			"endpoints": endpoints,
		},
	})
}

func (s *Service) lookupService(name string, r *http.Request) (*svcEntry, bool) {
	ns, ok := s.st.Namespaces[s.namespaceName(r)]
	if !ok {
		return nil, false
	}

	svc, ok := ns.Services[name]

	return svc, ok
}

func (s *Service) deleteService(w http.ResponseWriter, r *http.Request) {
	nsName := s.namespaceName(r)
	name := nsName + "/services/" + r.PathValue("service")

	s.mu.Lock()
	defer s.mu.Unlock()

	ns, ok := s.st.Namespaces[nsName]
	if !ok {
		httpx.NotFound(w, "namespace not found: "+nsName)

		return
	}

	if _, ok := ns.Services[name]; !ok {
		httpx.NotFound(w, "service not found: "+name)

		return
	}

	delete(ns.Services, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Endpoints ----

func (s *Service) serviceName(r *http.Request) string {
	return s.namespaceName(r) + "/services/" + r.PathValue("service")
}

func (s *Service) createEndpoint(w http.ResponseWriter, r *http.Request) {
	svcName := s.serviceName(r)
	id := r.URL.Query().Get("endpointId")

	var body endpoint
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if id == "" {
		httpx.BadRequest(w, "endpointId query parameter is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	ns, ok := s.st.Namespaces[s.namespaceName(r)]
	if !ok {
		httpx.NotFound(w, "namespace not found")

		return
	}

	svc, ok := ns.Services[svcName]
	if !ok {
		httpx.NotFound(w, "service not found: "+svcName)

		return
	}

	name := svcName + "/endpoints/" + id
	if _, exists := svc.Endpoints[name]; exists {
		httpx.AlreadyExists(w, "endpoint already exists: "+name)

		return
	}

	body.Name = name
	svc.Endpoints[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listEndpoints(w http.ResponseWriter, r *http.Request) {
	svcName := s.serviceName(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.lookupService(svcName, r)
	if !ok {
		httpx.NotFound(w, "service not found: "+svcName)

		return
	}

	names := make([]string, 0, len(svc.Endpoints))
	for n := range svc.Endpoints {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*endpoint, 0, len(names))
	for _, n := range names {
		items = append(items, svc.Endpoints[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"endpoints": items})
}

func (s *Service) getEndpoint(w http.ResponseWriter, r *http.Request) {
	svcName := s.serviceName(r)
	name := svcName + "/endpoints/" + r.PathValue("endpoint")

	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.lookupService(svcName, r)
	if !ok {
		httpx.NotFound(w, "service not found: "+svcName)

		return
	}

	ep, ok := svc.Endpoints[name]
	if !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, ep)
}

func (s *Service) deleteEndpoint(w http.ResponseWriter, r *http.Request) {
	svcName := s.serviceName(r)
	name := svcName + "/endpoints/" + r.PathValue("endpoint")

	s.mu.Lock()
	defer s.mu.Unlock()

	svc, ok := s.lookupService(svcName, r)
	if !ok {
		httpx.NotFound(w, "service not found: "+svcName)

		return
	}

	if _, ok := svc.Endpoints[name]; !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	delete(svc.Endpoints, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
