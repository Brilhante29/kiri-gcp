// Package serviceusage emulates Service Usage (serviceusage.googleapis.com/v1):
// per-project API enablement state.
package serviceusage

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "serviceusage"

// commonServices seeds every project with a realistic default set of
// already-enabled APIs, matching what a fresh GCP project ships with.
var commonServices = []string{
	"cloudresourcemanager.googleapis.com",
	"serviceusage.googleapis.com",
	"iam.googleapis.com",
}

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type state struct {
	// Enabled maps "project:service" -> true for enabled APIs.
	Enabled map[string]bool `json:"enabled"`
}

// Service implements the Service Usage emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Service Usage store.
func New() *Service { return &Service{st: state{Enabled: map[string]bool{}}} }

func (s *Service) ensureMaps() {
	if s.st.Enabled == nil {
		s.st.Enabled = map[string]bool{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Service Usage",
		Category:    "Management & Billing",
		Description: "Enable, disable, and inspect per-project API services",
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

// RegisterRoutes registers the Service Usage REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/services"
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{svc}", s.get)
	// {svc} also matches "name:enable" / "name:disable".
	r.Handle("POST", base+"/{svc}", s.action)
}

func (s *Service) key(project, svcName string) string { return project + ":" + svcName }

func (s *Service) isEnabled(project, svcName string) bool {
	if s.st.Enabled[s.key(project, svcName)] {
		return true
	}

	for _, c := range commonServices {
		if c == svcName {
			return true
		}
	}

	return false
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := map[string]bool{}
	for _, c := range commonServices {
		names[c] = true
	}

	prefix := project + ":"
	for k, enabled := range s.st.Enabled {
		if enabled && strings.HasPrefix(k, prefix) {
			names[strings.TrimPrefix(k, prefix)] = true
		}
	}

	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}

	sort.Strings(sorted)

	items := make([]map[string]any, 0, len(sorted))
	for _, n := range sorted {
		items = append(items, map[string]any{
			"name":  "projects/" + project + "/services/" + n,
			"state": "ENABLED",
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	svcName := r.PathValue("svc")

	s.mu.RLock()
	enabled := s.isEnabled(project, svcName)
	s.mu.RUnlock()

	state := "DISABLED"
	if enabled {
		state = "ENABLED"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":  "projects/" + project + "/services/" + svcName,
		"state": state,
	})
}

func (s *Service) action(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	svcName, verb := httpx.SplitVerb(r.PathValue("svc"))
	key := s.key(project, svcName)

	s.mu.Lock()
	defer s.mu.Unlock()

	switch verb {
	case "enable":
		s.st.Enabled[key] = true
	case "disable":
		s.st.Enabled[key] = false
	default:
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	state := "DISABLED"
	if s.isEnabled(project, svcName) {
		state = "ENABLED"
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":  "projects/" + project + "/services/" + svcName,
		"state": state,
	})
}
