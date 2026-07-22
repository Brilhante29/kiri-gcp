// Package iam emulates Cloud IAM's project-level surface
// (iam.googleapis.com/v1): service accounts, their keys, and the predefined
// role catalog. Resource-level policy bindings (setIamPolicy/getIamPolicy)
// are handled separately by the iampolicy package.
package iam

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "iam"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type key struct {
	Name        string `json:"name"`
	KeyType     string `json:"keyType,omitempty"`
	PrivateData string `json:"privateKeyData,omitempty"`
}

type serviceAccount struct {
	Name        string          `json:"name"`
	Email       string          `json:"email"`
	DisplayName string          `json:"displayName,omitempty"`
	UniqueID    string          `json:"uniqueId"`
	Keys        map[string]*key `json:"keys"`
}

type state struct {
	ServiceAccounts map[string]*serviceAccount `json:"serviceAccounts"` // full resource name -> SA
}

// Service implements the IAM (project-level) emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty IAM store.
func New() *Service { return &Service{st: state{ServiceAccounts: map[string]*serviceAccount{}}} }

func (s *Service) ensureMaps() {
	if s.st.ServiceAccounts == nil {
		s.st.ServiceAccounts = map[string]*serviceAccount{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "IAM",
		Category:    "Security",
		Description: "Service accounts, keys, and the predefined role catalog",
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

// RegisterRoutes registers the IAM REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/serviceAccounts"
	r.Handle("POST", base, s.createSA)
	r.Handle("GET", base, s.listSA)
	r.Handle("GET", base+"/{sa}", s.getSA)
	r.Handle("DELETE", base+"/{sa}", s.deleteSA)

	keyBase := base + "/{sa}/keys"
	r.Handle("POST", keyBase, s.createKey)
	r.Handle("GET", keyBase, s.listKeys)
	r.Handle("DELETE", keyBase+"/{key}", s.deleteKey)

	r.Handle("GET", "/v1/roles", s.listRoles)
	r.Handle("GET", "/v1/roles/{role}", s.getRole)
}

func (s *Service) saPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/serviceAccounts/"
}

func (s *Service) createSA(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body struct {
		AccountID      string          `json:"accountId"`
		ServiceAccount *serviceAccount `json:"serviceAccount"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.AccountID == "" {
		httpx.BadRequest(w, "accountId is required")

		return
	}

	email := body.AccountID + "@" + project + ".iam.gserviceaccount.com"
	name := s.saPrefix(r) + email

	sa := &serviceAccount{Name: name, Email: email, UniqueID: httpx.NumericID(), Keys: map[string]*key{}}
	if body.ServiceAccount != nil {
		sa.DisplayName = body.ServiceAccount.DisplayName
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.ServiceAccounts[name]; exists {
		httpx.AlreadyExists(w, "service account already exists: "+name)

		return
	}

	s.st.ServiceAccounts[name] = sa

	httpx.WriteJSON(w, http.StatusOK, sa)
}

func (s *Service) listSA(w http.ResponseWriter, r *http.Request) {
	prefix := s.saPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.ServiceAccounts {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*serviceAccount, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.ServiceAccounts[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"accounts": items})
}

func (s *Service) getSA(w http.ResponseWriter, r *http.Request) {
	name := s.saPrefix(r) + r.PathValue("sa")

	s.mu.RLock()
	sa, ok := s.st.ServiceAccounts[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "service account not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, sa)
}

func (s *Service) deleteSA(w http.ResponseWriter, r *http.Request) {
	name := s.saPrefix(r) + r.PathValue("sa")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.ServiceAccounts[name]; !ok {
		httpx.NotFound(w, "service account not found: "+name)

		return
	}

	delete(s.st.ServiceAccounts, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createKey(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sa, ok := s.st.ServiceAccounts[s.saPrefix(r)+r.PathValue("sa")]
	if !ok {
		httpx.NotFound(w, "service account not found")

		return
	}

	id := httpx.ID(16)
	k := &key{
		Name:        sa.Name + "/keys/" + id,
		KeyType:     "USER_MANAGED",
		PrivateData: httpx.ID(32),
	}
	sa.Keys[id] = k

	httpx.WriteJSON(w, http.StatusOK, k)
}

func (s *Service) listKeys(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	sa, ok := s.st.ServiceAccounts[s.saPrefix(r)+r.PathValue("sa")]
	if !ok {
		httpx.NotFound(w, "service account not found")

		return
	}

	ids := make([]string, 0, len(sa.Keys))
	for id := range sa.Keys {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*key, 0, len(ids))
	for _, id := range ids {
		items = append(items, sa.Keys[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"keys": items})
}

func (s *Service) deleteKey(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sa, ok := s.st.ServiceAccounts[s.saPrefix(r)+r.PathValue("sa")]
	if !ok {
		httpx.NotFound(w, "service account not found")

		return
	}

	if _, ok := sa.Keys[r.PathValue("key")]; !ok {
		httpx.NotFound(w, "key not found: "+r.PathValue("key"))

		return
	}

	delete(sa.Keys, r.PathValue("key"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// predefinedRoles is a small, representative slice of GCP's predefined role
// catalog — enough for clients that enumerate roles without needing the full
// production list of several thousand.
var predefinedRoles = []map[string]any{
	{"name": "roles/owner", "title": "Owner", "description": "Full access to all resources"},
	{"name": "roles/editor", "title": "Editor", "description": "Edit access to all resources"},
	{"name": "roles/viewer", "title": "Viewer", "description": "Read access to all resources"},
	{"name": "roles/iam.serviceAccountUser", "title": "Service Account User", "description": "Run operations as a service account"},
	{"name": "roles/storage.admin", "title": "Storage Admin", "description": "Full control of GCS buckets and objects"},
	{"name": "roles/pubsub.editor", "title": "Pub/Sub Editor", "description": "Edit access to topics and subscriptions"},
	{"name": "roles/cloudsql.admin", "title": "Cloud SQL Admin", "description": "Full control of Cloud SQL resources"},
}

func (s *Service) listRoles(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"roles": predefinedRoles})
}

func (s *Service) getRole(w http.ResponseWriter, r *http.Request) {
	name := "roles/" + r.PathValue("role")

	for _, role := range predefinedRoles {
		if role["name"] == name {
			httpx.WriteJSON(w, http.StatusOK, role)

			return
		}
	}

	httpx.NotFound(w, "role not found: "+name)
}
