// Package identityplatform emulates Identity Platform
// (identitytoolkit.googleapis.com/v2): tenants and the users within each.
package identityplatform

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "identityplatform"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type user struct {
	LocalID  string `json:"localId"`
	Email    string `json:"email,omitempty"`
	Disabled bool   `json:"disabled"`
}

type tenant struct {
	Name        string           `json:"name"`
	DisplayName string           `json:"displayName,omitempty"`
	Users       map[string]*user `json:"users"`
}

type state struct {
	Tenants map[string]*tenant `json:"tenants"` // full path -> tenant
}

// Service implements the Identity Platform emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Identity Platform store.
func New() *Service { return &Service{st: state{Tenants: map[string]*tenant{}}} }

func (s *Service) ensureMaps() {
	if s.st.Tenants == nil {
		s.st.Tenants = map[string]*tenant{}
	}

	for _, t := range s.st.Tenants {
		if t.Users == nil {
			t.Users = map[string]*user{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Identity Platform",
		Category:    "Security",
		Description: "Customer identity tenants and users",
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

// RegisterRoutes registers the Identity Platform REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v2/projects/{project}/tenants"
	r.Handle("POST", base, s.createTenant)
	r.Handle("GET", base, s.listTenants)
	r.Handle("GET", base+"/{tenant}", s.getTenant)
	r.Handle("DELETE", base+"/{tenant}", s.deleteTenant)

	userBase := base + "/{tenant}/users"
	r.Handle("POST", userBase, s.createUser)
	r.Handle("GET", userBase, s.listUsers)
	r.Handle("DELETE", userBase+"/{user}", s.deleteUser)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/tenants/"
}

func (s *Service) tenantName(r *http.Request) string {
	return s.prefix(r) + r.PathValue("tenant")
}

// ---- Tenants ----

func (s *Service) createTenant(w http.ResponseWriter, r *http.Request) {
	var body tenant
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	id := httpx.ID(8)
	name := s.prefix(r) + id
	body.Name = name
	body.Users = map[string]*user{}

	s.mu.Lock()
	s.st.Tenants[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listTenants(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Tenants {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*tenant, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Tenants[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tenants": items})
}

func (s *Service) getTenant(w http.ResponseWriter, r *http.Request) {
	name := s.tenantName(r)

	s.mu.RLock()
	t, ok := s.st.Tenants[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "tenant not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTenant(w http.ResponseWriter, r *http.Request) {
	name := s.tenantName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Tenants[name]; !ok {
		httpx.NotFound(w, "tenant not found: "+name)

		return
	}

	delete(s.st.Tenants, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Users ----

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	var body user
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.st.Tenants[s.tenantName(r)]
	if !ok {
		httpx.NotFound(w, "tenant not found: "+s.tenantName(r))

		return
	}

	body.LocalID = httpx.ID(8)
	t.Users[body.LocalID] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.st.Tenants[s.tenantName(r)]
	if !ok {
		httpx.NotFound(w, "tenant not found: "+s.tenantName(r))

		return
	}

	ids := make([]string, 0, len(t.Users))
	for id := range t.Users {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*user, 0, len(ids))
	for _, id := range ids {
		items = append(items, t.Users[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"users": items})
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.st.Tenants[s.tenantName(r)]
	if !ok {
		httpx.NotFound(w, "tenant not found: "+s.tenantName(r))

		return
	}

	if _, ok := t.Users[r.PathValue("user")]; !ok {
		httpx.NotFound(w, "user not found: "+r.PathValue("user"))

		return
	}

	delete(t.Users, r.PathValue("user"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
