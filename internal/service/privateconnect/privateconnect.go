// Package privateconnect emulates Private Service Connect
// (compute.googleapis.com/v1): service attachments (the producer side) and
// forwarding-rule-based endpoints that connect to them (the consumer side).
package privateconnect

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "privateconnect"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type serviceAttachment struct {
	Name                 string   `json:"name"`
	TargetService        string   `json:"targetService,omitempty"`
	ConnectionPreference string   `json:"connectionPreference,omitempty"`
	ConnectedEndpoints   []string `json:"connectedEndpoints,omitempty"`
}

type endpoint struct {
	Name            string `json:"name"`
	Target          string `json:"target"` // service attachment name
	PSCConnectionID string `json:"pscConnectionId"`
	State           string `json:"state"`
}

type state struct {
	Attachments map[string]*serviceAttachment `json:"attachments"` // full path -> attachment
	Endpoints   map[string]*endpoint          `json:"endpoints"`   // full path -> endpoint
}

// Service implements the Private Service Connect emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Private Service Connect store.
func New() *Service {
	return &Service{st: state{Attachments: map[string]*serviceAttachment{}, Endpoints: map[string]*endpoint{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Attachments == nil {
		s.st.Attachments = map[string]*serviceAttachment{}
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
		Display:     "Private Service Connect",
		Category:    "Networking",
		Description: "Service attachments and consumer endpoints for private connectivity",
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

// RegisterRoutes registers the Private Service Connect REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	saBase := "/compute/v1/projects/{project}/regions/{region}/serviceAttachments"
	r.Handle("POST", saBase, s.createAttachment)
	r.Handle("GET", saBase, s.listAttachments)
	r.Handle("GET", saBase+"/{name}", s.getAttachment)
	r.Handle("DELETE", saBase+"/{name}", s.deleteAttachment)

	epBase := "/compute/v1/projects/{project}/regions/{region}/forwardingRules"
	r.Handle("POST", epBase, s.createEndpoint)
	r.Handle("GET", epBase, s.listEndpoints)
	r.Handle("GET", epBase+"/{name}", s.getEndpoint)
	r.Handle("DELETE", epBase+"/{name}", s.deleteEndpoint)
}

func (s *Service) saPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/serviceAttachments/"
}

func (s *Service) epPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/regions/" + r.PathValue("region") + "/forwardingRules/"
}

// ---- Service attachments (producer side) ----

func (s *Service) createAttachment(w http.ResponseWriter, r *http.Request) {
	var body serviceAttachment
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.saPrefix(r) + body.Name
	if body.ConnectionPreference == "" {
		body.ConnectionPreference = "ACCEPT_AUTOMATIC"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Attachments[name]; exists {
		httpx.AlreadyExists(w, "service attachment already exists: "+name)

		return
	}

	s.st.Attachments[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listAttachments(w http.ResponseWriter, r *http.Request) {
	prefix := s.saPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Attachments {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*serviceAttachment, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Attachments[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getAttachment(w http.ResponseWriter, r *http.Request) {
	name := s.saPrefix(r) + r.PathValue("name")

	s.mu.RLock()
	a, ok := s.st.Attachments[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "service attachment not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) deleteAttachment(w http.ResponseWriter, r *http.Request) {
	name := s.saPrefix(r) + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Attachments[name]; !ok {
		httpx.NotFound(w, "service attachment not found: "+name)

		return
	}

	delete(s.st.Attachments, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Endpoints (consumer side) ----

func (s *Service) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var body endpoint
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" || body.Target == "" {
		httpx.BadRequest(w, "name and target are required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	attachment, ok := s.st.Attachments[body.Target]
	if !ok {
		httpx.NotFound(w, "target service attachment not found: "+body.Target)

		return
	}

	name := s.epPrefix(r) + body.Name
	body.PSCConnectionID = httpx.NumericID()
	body.State = "ACCEPTED"

	attachment.ConnectedEndpoints = append(attachment.ConnectedEndpoints, name)
	s.st.Endpoints[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listEndpoints(w http.ResponseWriter, r *http.Request) {
	prefix := s.epPrefix(r)

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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getEndpoint(w http.ResponseWriter, r *http.Request) {
	name := s.epPrefix(r) + r.PathValue("name")

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
	name := s.epPrefix(r) + r.PathValue("name")

	s.mu.Lock()
	defer s.mu.Unlock()

	e, ok := s.st.Endpoints[name]
	if !ok {
		httpx.NotFound(w, "endpoint not found: "+name)

		return
	}

	if a, ok := s.st.Attachments[e.Target]; ok {
		filtered := a.ConnectedEndpoints[:0]

		for _, n := range a.ConnectedEndpoints {
			if n != name {
				filtered = append(filtered, n)
			}
		}

		a.ConnectedEndpoints = filtered
	}

	delete(s.st.Endpoints, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
