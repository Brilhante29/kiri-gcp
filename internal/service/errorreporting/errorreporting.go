// Package errorreporting emulates Error Reporting
// (clouderrorreporting.googleapis.com/v1beta1): reported error events,
// aggregated into groups by their message.
package errorreporting

import (
	"net/http"
	"sort"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "errorreporting"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type errorEvent struct {
	Message        string         `json:"message"`
	EventTime      string         `json:"eventTime"`
	ServiceContext map[string]any `json:"serviceContext,omitempty"`
}

type errorGroup struct {
	GroupID string        `json:"groupId"`
	Count   int           `json:"count"`
	Events  []*errorEvent `json:"events"`
}

type state struct {
	Groups map[string]*errorGroup `json:"groups"` // "project:message" -> group
}

// Service implements the Error Reporting emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Error Reporting store.
func New() *Service { return &Service{st: state{Groups: map[string]*errorGroup{}}} }

func (s *Service) ensureMaps() {
	if s.st.Groups == nil {
		s.st.Groups = map[string]*errorGroup{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Error Reporting",
		Category:    "Monitoring & Logging",
		Description: "Reported error events aggregated into groups",
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

// RegisterRoutes registers the Error Reporting REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1beta1/projects/{project}/events:report", s.reportEvent)
	r.Handle("GET", "/v1beta1/projects/{project}/groupStats", s.listGroups)
}

func (s *Service) reportEvent(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body errorEvent
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Message == "" {
		httpx.BadRequest(w, "message is required")

		return
	}

	if body.EventTime == "" {
		body.EventTime = httpx.Now()
	}

	key := project + ":" + body.Message

	s.mu.Lock()
	defer s.mu.Unlock()

	g, ok := s.st.Groups[key]
	if !ok {
		g = &errorGroup{GroupID: httpx.ID(8)}
		s.st.Groups[key] = g
	}

	g.Count++
	g.Events = append(g.Events, &body)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) listGroups(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	s.mu.RLock()
	defer s.mu.RUnlock()

	keys := make([]string, 0)

	for k := range s.st.Groups {
		if len(k) > len(project) && k[:len(project)+1] == project+":" {
			keys = append(keys, k)
		}
	}

	sort.Strings(keys)

	items := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		g := s.st.Groups[k]
		items = append(items, map[string]any{
			"group": map[string]any{"groupId": g.GroupID},
			"count": g.Count,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"errorGroupStats": items})
}
