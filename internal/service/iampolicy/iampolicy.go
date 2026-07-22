// Package iampolicy emulates Cloud IAM's resource-level policy surface
// (google.iam.v1.IAMPolicy): setIamPolicy, getIamPolicy, and
// testIamPermissions. In real GCP these three custom methods are exposed by
// every resource-owning service at its own resource path
// (".../topics/{t}:setIamPolicy", ".../secrets/{s}:setIamPolicy", ...), not by
// a separate service. This package emulates that by listening on a generic
// "/v1/{resource...}" POST pattern and dispatching on the trailing ":verb".
//
// Only POST is registered, deliberately. Real GetIamPolicy is a GET, but
// every one of the ~86 generic-CRUD services already registers its own
// "GET .../{service}/{id}" route, which is strictly more specific (more
// literal path segments) than this catch-all and therefore always wins Go's
// http.ServeMux routing — a GET catch-all here would work for some resources
// and silently 404 through the owning service's own getItem handler for
// others. Calling getIamPolicy via POST (which the real API also accepts)
// avoids that inconsistency entirely, since none of those services register
// POST at their {id} path (only at the collection path).
//
// Resource paths that another service already registers a POST handler for
// at the exact {id} path (pubsub topics/subscriptions, secretmanager
// secrets, servicedirectory endpoints) are still intercepted by that service
// first — IAM policy on those specific resources is not yet wired through.
// Every other resource (the generic CRUD services, billing accounts, org
// policies, etc.) resolves here correctly via POST.
package iampolicy

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "iampolicy"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type binding struct {
	Role    string   `json:"role"`
	Members []string `json:"members"`
}

type policy struct {
	Version  int        `json:"version"`
	Bindings []*binding `json:"bindings"`
	Etag     string     `json:"etag"`
}

type state struct {
	Policies map[string]*policy `json:"policies"` // resource name -> policy
}

// Service implements the generic resource-level IAM policy store.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty IAM policy store.
func New() *Service { return &Service{st: state{Policies: map[string]*policy{}}} }

func (s *Service) ensureMaps() {
	if s.st.Policies == nil {
		s.st.Policies = map[string]*policy{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "IAM Policy",
		Category:    "Security",
		Description: "Resource-level IAM policy bindings (setIamPolicy/getIamPolicy/testIamPermissions)",
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

// RegisterRoutes registers the generic IAM policy dispatch route.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/{resource...}", s.dispatch)
}

func (s *Service) dispatch(w http.ResponseWriter, r *http.Request) {
	resource, verb := httpx.SplitVerb(r.PathValue("resource"))

	switch {
	case verb == "setIamPolicy" && r.Method == http.MethodPost:
		s.setIamPolicy(w, r, resource)
	case verb == "getIamPolicy" && (r.Method == http.MethodGet || r.Method == http.MethodPost):
		s.getIamPolicy(w, resource)
	case verb == "testIamPermissions" && r.Method == http.MethodPost:
		s.testIamPermissions(w, r, resource)
	default:
		httpx.NotFound(w, "no route for "+r.Method+" /v1/"+r.PathValue("resource"))
	}
}

func (s *Service) setIamPolicy(w http.ResponseWriter, r *http.Request, resource string) {
	var body struct {
		Policy policy `json:"policy"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Policy.Version == 0 {
		body.Policy.Version = 1
	}

	body.Policy.Etag = httpx.ID(8)

	s.mu.Lock()
	s.st.Policies[resource] = &body.Policy
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body.Policy)
}

func (s *Service) getIamPolicy(w http.ResponseWriter, resource string) {
	s.mu.RLock()
	p, ok := s.st.Policies[resource]
	s.mu.RUnlock()

	if !ok {
		// A resource with no policy yet still has a valid (empty) policy in
		// real GCP, rather than a 404 — mirror that.
		httpx.WriteJSON(w, http.StatusOK, policy{Version: 1, Bindings: []*binding{}, Etag: httpx.ID(8)})

		return
	}

	httpx.WriteJSON(w, http.StatusOK, p)
}

func (s *Service) testIamPermissions(w http.ResponseWriter, r *http.Request, resource string) {
	var body struct {
		Permissions []string `json:"permissions"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.RLock()
	p := s.st.Policies[resource]
	s.mu.RUnlock()

	// Zero-auth emulator: every permission held by any bound member is
	// reported as granted. With no policy set, nothing is granted.
	granted := map[string]bool{}

	if p != nil {
		for _, b := range p.Bindings {
			for range b.Members {
				granted[b.Role] = true
			}
		}
	}

	allowed := make([]string, 0, len(body.Permissions))

	for _, perm := range body.Permissions {
		if roleGrantsPermission(granted, perm) {
			allowed = append(allowed, perm)
		}
	}

	sort.Strings(allowed)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"permissions": allowed})
}

// roleGrantsPermission is a coarse emulation: a bound role is treated as
// granting any permission whose service prefix matches the role's service
// segment (e.g. role "roles/pubsub.editor" grants "pubsub.topics.publish").
func roleGrantsPermission(granted map[string]bool, permission string) bool {
	service := strings.SplitN(permission, ".", 2)[0]

	for role := range granted {
		if strings.Contains(role, service) {
			return true
		}
	}

	return false
}
