// Package securitycommandcenter emulates Security Command Center
// (securitycenter.googleapis.com/v1): sources and the findings reported
// under them.
package securitycommandcenter

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "securitycommandcenter"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type finding struct {
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
	State    string `json:"state"`
	Severity string `json:"severity,omitempty"`
}

type source struct {
	Name        string              `json:"name"`
	DisplayName string              `json:"displayName,omitempty"`
	Findings    map[string]*finding `json:"findings"`
}

type state struct {
	Sources map[string]*source `json:"sources"` // full path -> source
}

// Service implements the Security Command Center emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Security Command Center store.
func New() *Service { return &Service{st: state{Sources: map[string]*source{}}} }

func (s *Service) ensureMaps() {
	if s.st.Sources == nil {
		s.st.Sources = map[string]*source{}
	}

	for _, src := range s.st.Sources {
		if src.Findings == nil {
			src.Findings = map[string]*finding{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Security Command Center",
		Category:    "Security",
		Description: "Sources and the security findings reported under them",
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

// RegisterRoutes registers the Security Command Center REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/organizations/{org}/sources"
	r.Handle("POST", base, s.createSource)
	r.Handle("GET", base, s.listSources)
	r.Handle("GET", base+"/{source}", s.getSource)

	findingBase := base + "/{source}/findings"
	r.Handle("POST", findingBase, s.createFinding)
	r.Handle("GET", findingBase, s.listFindings)
	r.Handle("GET", findingBase+"/{finding}", s.getFinding)
}

func (s *Service) prefix(r *http.Request) string {
	return "organizations/" + r.PathValue("org") + "/sources/"
}

func (s *Service) sourceName(r *http.Request) string {
	return s.prefix(r) + r.PathValue("source")
}

func (s *Service) createSource(w http.ResponseWriter, r *http.Request) {
	var body source
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	id := httpx.ID(8)
	name := s.prefix(r) + id
	body.Name = name
	body.Findings = map[string]*finding{}

	s.mu.Lock()
	s.st.Sources[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listSources(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Sources {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*source, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Sources[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sources": items})
}

func (s *Service) getSource(w http.ResponseWriter, r *http.Request) {
	name := s.sourceName(r)

	s.mu.RLock()
	src, ok := s.st.Sources[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "source not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, src)
}

func (s *Service) createFinding(w http.ResponseWriter, r *http.Request) {
	findingID := r.URL.Query().Get("findingId")

	var body finding
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if findingID == "" {
		findingID = "finding-" + httpx.ID(4)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	src, ok := s.st.Sources[s.sourceName(r)]
	if !ok {
		httpx.NotFound(w, "source not found: "+s.sourceName(r))

		return
	}

	if _, exists := src.Findings[findingID]; exists {
		httpx.AlreadyExists(w, "finding already exists: "+findingID)

		return
	}

	body.Name = s.sourceName(r) + "/findings/" + findingID

	if body.State == "" {
		body.State = "ACTIVE"
	}

	src.Findings[findingID] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listFindings(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	src, ok := s.st.Sources[s.sourceName(r)]
	if !ok {
		httpx.NotFound(w, "source not found: "+s.sourceName(r))

		return
	}

	ids := make([]string, 0, len(src.Findings))
	for id := range src.Findings {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{"finding": src.Findings[id]})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"listFindingsResults": items})
}

func (s *Service) getFinding(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	src, ok := s.st.Sources[s.sourceName(r)]
	if !ok {
		httpx.NotFound(w, "source not found: "+s.sourceName(r))

		return
	}

	f, ok := src.Findings[r.PathValue("finding")]
	if !ok {
		httpx.NotFound(w, "finding not found: "+r.PathValue("finding"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, f)
}
