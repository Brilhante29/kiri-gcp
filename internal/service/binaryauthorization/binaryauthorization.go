// Package binaryauthorization emulates Binary Authorization
// (binaryauthorization.googleapis.com/v1): the single project-scoped policy
// resource, plus attestors.
package binaryauthorization

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "binaryauthorization"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type attestor struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type policy struct {
	Name                 string         `json:"name"`
	DefaultAdmissionRule map[string]any `json:"defaultAdmissionRule,omitempty"`
}

type state struct {
	Policies  map[string]*policy   `json:"policies"`  // "projects/{p}/policy" -> policy
	Attestors map[string]*attestor `json:"attestors"` // full path -> attestor
}

// Service implements the Binary Authorization emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Binary Authorization store.
func New() *Service {
	return &Service{st: state{Policies: map[string]*policy{}, Attestors: map[string]*attestor{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Policies == nil {
		s.st.Policies = map[string]*policy{}
	}

	if s.st.Attestors == nil {
		s.st.Attestors = map[string]*attestor{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Binary Authorization",
		Category:    "Security",
		Description: "Deploy-time container image attestation policy",
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

// RegisterRoutes registers the Binary Authorization REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("GET", "/v1/projects/{project}/policy", s.getPolicy)
	r.Handle("PUT", "/v1/projects/{project}/policy", s.updatePolicy)

	attBase := "/v1/projects/{project}/attestors"
	r.Handle("POST", attBase, s.createAttestor)
	r.Handle("GET", attBase, s.listAttestors)
	r.Handle("GET", attBase+"/{attestor}", s.getAttestor)
	r.Handle("DELETE", attBase+"/{attestor}", s.deleteAttestor)
}

func (s *Service) policyName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/policy"
}

func (s *Service) getPolicy(w http.ResponseWriter, r *http.Request) {
	name := s.policyName(r)

	s.mu.RLock()
	p, ok := s.st.Policies[name]
	s.mu.RUnlock()

	if !ok {
		// A project with no policy set yet still has a default (implicit
		// ALLOW) policy in real GCP, rather than a 404 — mirror that.
		httpx.WriteJSON(w, http.StatusOK, policy{
			Name:                 name,
			DefaultAdmissionRule: map[string]any{"evaluationMode": "ALWAYS_ALLOW", "enforcementMode": "ENFORCED_BLOCK_AND_AUDIT_LOG"},
		})

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) updatePolicy(w http.ResponseWriter, r *http.Request) {
	var body policy
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	body.Name = s.policyName(r)

	s.mu.Lock()
	s.st.Policies[body.Name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) attPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/attestors/"
}

func (s *Service) createAttestor(w http.ResponseWriter, r *http.Request) {
	var body attestor
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.attPrefix(r) + body.Name
	body.Name = name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Attestors[name]; exists {
		httpx.AlreadyExists(w, "attestor already exists: "+name)

		return
	}

	s.st.Attestors[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listAttestors(w http.ResponseWriter, r *http.Request) {
	prefix := s.attPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Attestors {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*attestor, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Attestors[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"attestors": items})
}

func (s *Service) getAttestor(w http.ResponseWriter, r *http.Request) {
	name := s.attPrefix(r) + r.PathValue("attestor")

	s.mu.RLock()
	a, ok := s.st.Attestors[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "attestor not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) deleteAttestor(w http.ResponseWriter, r *http.Request) {
	name := s.attPrefix(r) + r.PathValue("attestor")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Attestors[name]; !ok {
		httpx.NotFound(w, "attestor not found: "+name)

		return
	}

	delete(s.st.Attestors, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
