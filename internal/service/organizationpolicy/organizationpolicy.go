// Package organizationpolicy emulates the Org Policy API (orgpolicy.googleapis.com/v1)
// scoped to projects: listing, getting, creating, updating, and deleting policy
// constraints, plus resolving the effective policy for a constraint. Folder and
// organization parents are out of scope for this wave — only "projects/{project}"
// is supported, which covers the common local-development case.
//
// A constraint's REST identifier is itself compound — "constraints/{name}",
// e.g. "constraints/compute.disableSerialPortAccess" — so the routes below
// use a trailing wildcard to capture the full "constraints/..." segment
// rather than a single-segment {constraint}, which would only match the
// literal word "constraints" and drop the rest of the path.
package organizationpolicy

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "organizationpolicy"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type policySpec struct {
	Rules []map[string]any `json:"rules,omitempty"`
	Etag  string           `json:"etag,omitempty"`
}

type orgPolicy struct {
	Name string     `json:"name"` // projects/{p}/policies/{constraint}
	Spec policySpec `json:"spec"`
}

type state struct {
	Policies map[string]*orgPolicy `json:"policies"` // full name -> policy
}

// Service implements the project-scoped Org Policy emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Org Policy store.
func New() *Service { return &Service{st: state{Policies: map[string]*orgPolicy{}}} }

func (s *Service) ensureMaps() {
	if s.st.Policies == nil {
		s.st.Policies = map[string]*orgPolicy{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Organization Policy",
		Category:    "Security",
		Description: "Project-scoped resource configuration constraints",
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

// RegisterRoutes registers the Org Policy REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("GET", "/v1/projects/{project}/policies", s.list)
	r.Handle("POST", "/v1/projects/{project}/policies", s.create)
	r.Handle("GET", "/v1/projects/{project}/policies/{constraint...}", s.getOrResolve)
	r.Handle("PATCH", "/v1/projects/{project}/policies/{constraint...}", s.update)
	r.Handle("DELETE", "/v1/projects/{project}/policies/{constraint...}", s.delete)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := "projects/" + r.PathValue("project") + "/policies/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for name := range s.st.Policies {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	items := make([]*orgPolicy, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Policies[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"policies": items})
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body orgPolicy
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	constraint := strings.TrimPrefix(body.Name, "projects/"+project+"/policies/")
	if constraint == "" || constraint == body.Name {
		httpx.BadRequest(w, "name must be projects/{project}/policies/{constraint}")

		return
	}

	name := "projects/" + project + "/policies/" + constraint
	body.Name = name
	body.Spec.Etag = httpx.ID(8)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Policies[name]; exists {
		httpx.AlreadyExists(w, "policy already exists: "+name)

		return
	}

	s.st.Policies[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) getOrResolve(w http.ResponseWriter, r *http.Request) {
	constraint, verb := httpx.SplitVerb(r.PathValue("constraint"))
	name := "projects/" + r.PathValue("project") + "/policies/" + constraint

	s.mu.RLock()
	p, ok := s.st.Policies[name]
	s.mu.RUnlock()

	if verb == "getEffectivePolicy" {
		// No org/folder hierarchy is modeled, so the effective policy is
		// simply the project-level policy, or an empty rule set if unset.
		if !ok {
			httpx.WriteJSON(w, http.StatusOK, policySpec{Rules: []map[string]any{}})

			return
		}

		httpx.WriteJSON(w, http.StatusOK, p.Spec)

		return
	}

	if !ok {
		httpx.NotFound(w, "policy not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) update(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/policies/" + r.PathValue("constraint")

	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.st.Policies[name]
	if !ok {
		httpx.NotFound(w, "policy not found: "+name)

		return
	}

	var patch orgPolicy
	if err := httpx.DecodeJSON(r, &patch); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if patch.Spec.Rules != nil {
		p.Spec.Rules = patch.Spec.Rules
	}

	p.Spec.Etag = httpx.ID(8)

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/policies/" + r.PathValue("constraint")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Policies[name]; !ok {
		httpx.NotFound(w, "policy not found: "+name)

		return
	}

	delete(s.st.Policies, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
