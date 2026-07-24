// Package monitoring emulates Cloud Monitoring (monitoring.googleapis.com/v3):
// metric descriptors, time series ingestion, and alert policies.
package monitoring

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "monitoring"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type metricDescriptor struct {
	Name       string `json:"name"`
	Type       string `json:"type,omitempty"`
	MetricKind string `json:"metricKind,omitempty"`
	ValueType  string `json:"valueType,omitempty"`
}

type timeSeriesPoint struct {
	Metric map[string]any   `json:"metric"`
	Points []map[string]any `json:"points"`
}

type alertPolicy struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type state struct {
	Descriptors map[string]*metricDescriptor `json:"descriptors"` // full path -> descriptor
	TimeSeries  []*timeSeriesPoint           `json:"timeSeries"`
	Alerts      map[string]*alertPolicy      `json:"alerts"` // full path -> policy
}

// Service implements the Cloud Monitoring emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Monitoring store.
func New() *Service {
	return &Service{st: state{
		Descriptors: map[string]*metricDescriptor{},
		Alerts:      map[string]*alertPolicy{},
	}}
}

func (s *Service) ensureMaps() {
	if s.st.Descriptors == nil {
		s.st.Descriptors = map[string]*metricDescriptor{}
	}

	if s.st.Alerts == nil {
		s.st.Alerts = map[string]*alertPolicy{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Monitoring",
		Category:    "Monitoring & Logging",
		Description: "Metric descriptors, time series ingestion, and alert policies",
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

// RegisterRoutes registers the Cloud Monitoring REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	descBase := "/v3/projects/{project}/metricDescriptors"
	r.Handle("POST", descBase, s.createDescriptor)
	r.Handle("GET", descBase, s.listDescriptors)
	r.Handle("GET", descBase+"/{descriptor}", s.getDescriptor)
	r.Handle("DELETE", descBase+"/{descriptor}", s.deleteDescriptor)

	r.Handle("POST", "/v3/projects/{project}/timeSeries", s.createTimeSeries)
	r.Handle("GET", "/v3/projects/{project}/timeSeries", s.listTimeSeries)

	alertBase := "/v3/projects/{project}/alertPolicies"
	r.Handle("POST", alertBase, s.createAlert)
	r.Handle("GET", alertBase, s.listAlerts)
	r.Handle("GET", alertBase+"/{alert}", s.getAlert)
	r.Handle("DELETE", alertBase+"/{alert}", s.deleteAlert)
}

func (s *Service) descPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/metricDescriptors/"
}

func (s *Service) createDescriptor(w http.ResponseWriter, r *http.Request) {
	var body metricDescriptor
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Type == "" {
		httpx.BadRequest(w, "type is required")

		return
	}

	name := s.descPrefix(r) + body.Type
	body.Name = name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Descriptors[name]; exists {
		httpx.AlreadyExists(w, "metric descriptor already exists: "+name)

		return
	}

	s.st.Descriptors[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listDescriptors(w http.ResponseWriter, r *http.Request) {
	prefix := s.descPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Descriptors {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*metricDescriptor, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Descriptors[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"metricDescriptors": items})
}

func (s *Service) getDescriptor(w http.ResponseWriter, r *http.Request) {
	name := s.descPrefix(r) + r.PathValue("descriptor")

	s.mu.RLock()
	d, ok := s.st.Descriptors[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "metric descriptor not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, d)
}

func (s *Service) deleteDescriptor(w http.ResponseWriter, r *http.Request) {
	name := s.descPrefix(r) + r.PathValue("descriptor")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Descriptors[name]; !ok {
		httpx.NotFound(w, "metric descriptor not found: "+name)

		return
	}

	delete(s.st.Descriptors, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createTimeSeries(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TimeSeries []*timeSeriesPoint `json:"timeSeries"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	s.st.TimeSeries = append(s.st.TimeSeries, body.TimeSeries...)
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) listTimeSeries(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"timeSeries": s.st.TimeSeries})
}

func (s *Service) alertPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/alertPolicies/"
}

func (s *Service) createAlert(w http.ResponseWriter, r *http.Request) {
	var body alertPolicy
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	name := s.alertPrefix(r) + httpx.ID(8)
	body.Name = name
	body.Enabled = true

	s.mu.Lock()
	s.st.Alerts[name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listAlerts(w http.ResponseWriter, r *http.Request) {
	prefix := s.alertPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Alerts {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*alertPolicy, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Alerts[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"alertPolicies": items})
}

func (s *Service) getAlert(w http.ResponseWriter, r *http.Request) {
	name := s.alertPrefix(r) + r.PathValue("alert")

	s.mu.RLock()
	a, ok := s.st.Alerts[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "alert policy not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) deleteAlert(w http.ResponseWriter, r *http.Request) {
	name := s.alertPrefix(r) + r.PathValue("alert")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Alerts[name]; !ok {
		httpx.NotFound(w, "alert policy not found: "+name)

		return
	}

	delete(s.st.Alerts, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
