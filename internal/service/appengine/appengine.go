// Package appengine emulates App Engine Admin (appengine.googleapis.com/v1):
// services and the versions deployed to them, with a traffic split on the
// service tracking which version is live.
package appengine

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "appengine"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type version struct {
	ID            string `json:"id"`
	ServingStatus string `json:"servingStatus"`
	Runtime       string `json:"runtime,omitempty"`
}

type appService struct {
	ID       string              `json:"id"`
	Split    map[string]float64  `json:"split,omitempty"`
	Versions map[string]*version `json:"versions"`
}

type state struct {
	Services map[string]*appService `json:"services"` // "project:serviceId" -> service
}

// Service implements the App Engine Admin emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty App Engine store.
func New() *Service { return &Service{st: state{Services: map[string]*appService{}}} }

func (s *Service) ensureMaps() {
	if s.st.Services == nil {
		s.st.Services = map[string]*appService{}
	}

	for _, svc := range s.st.Services {
		if svc.Versions == nil {
			svc.Versions = map[string]*version{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "App Engine",
		Category:    "Compute",
		Description: "Services and versions with traffic splitting",
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

// RegisterRoutes registers the App Engine REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/apps/{project}/services"
	r.Handle("GET", base, s.listServices)
	r.Handle("GET", base+"/{svc}", s.getService)

	verBase := base + "/{svc}/versions"
	r.Handle("POST", verBase, s.createVersion)
	r.Handle("GET", verBase, s.listVersions)
	r.Handle("GET", verBase+"/{version}", s.getVersion)
	r.Handle("DELETE", verBase+"/{version}", s.deleteVersion)
}

func (s *Service) key(project, svcID string) string { return project + ":" + svcID }

func (s *Service) getOrCreateService(project, svcID string) *appService {
	key := s.key(project, svcID)

	svc, ok := s.st.Services[key]
	if !ok {
		svc = &appService{ID: svcID, Split: map[string]float64{}, Versions: map[string]*version{}}
		s.st.Services[key] = svc
	}

	return svc
}

func (s *Service) listServices(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	prefix := project + ":"

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)
	for k := range s.st.Services {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]*appService, 0, len(keys))
	for _, k := range keys {
		items = append(items, s.st.Services[k])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *Service) getService(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	svc, ok := s.st.Services[s.key(r.PathValue("project"), r.PathValue("svc"))]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "service not found: "+r.PathValue("svc"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, svc)
}

func (s *Service) createVersion(w http.ResponseWriter, r *http.Request) {
	var body version
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.ID == "" {
		body.ID = "v-" + httpx.ID(4)
	}

	body.ServingStatus = "SERVING"

	s.mu.Lock()
	defer s.mu.Unlock()

	svc := s.getOrCreateService(r.PathValue("project"), r.PathValue("svc"))
	if _, exists := svc.Versions[body.ID]; exists {
		httpx.AlreadyExists(w, "version already exists: "+body.ID)

		return
	}

	svc.Versions[body.ID] = &body
	svc.Split = map[string]float64{body.ID: 1.0}

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listVersions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.st.Services[s.key(r.PathValue("project"), r.PathValue("svc"))]
	if !ok {
		httpx.NotFound(w, "service not found: "+r.PathValue("svc"))

		return
	}

	ids := make([]string, 0, len(svc.Versions))
	for id := range svc.Versions {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]*version, 0, len(ids))
	for _, id := range ids {
		items = append(items, svc.Versions[id])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"versions": items})
}

func (s *Service) getVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	svc, ok := s.st.Services[s.key(r.PathValue("project"), r.PathValue("svc"))]
	if !ok {
		httpx.NotFound(w, "service not found: "+r.PathValue("svc"))

		return
	}

	v, ok := svc.Versions[r.PathValue("version")]
	if !ok {
		httpx.NotFound(w, "version not found: "+r.PathValue("version"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, v)
}

func (s *Service) deleteVersion(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	svc, ok := s.st.Services[s.key(r.PathValue("project"), r.PathValue("svc"))]
	if !ok {
		httpx.NotFound(w, "service not found: "+r.PathValue("svc"))

		return
	}

	if _, ok := svc.Versions[r.PathValue("version")]; !ok {
		httpx.NotFound(w, "version not found: "+r.PathValue("version"))

		return
	}

	delete(svc.Versions, r.PathValue("version"))
	delete(svc.Split, r.PathValue("version"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
