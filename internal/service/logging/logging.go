// Package logging emulates Cloud Logging (logging.googleapis.com/v2): log
// entry write/list, log-based metrics, and sinks.
package logging

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "logging"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type logEntry struct {
	LogName     string         `json:"logName,omitempty"`
	Severity    string         `json:"severity,omitempty"`
	TextPayload string         `json:"textPayload,omitempty"`
	JSONPayload map[string]any `json:"jsonPayload,omitempty"`
	Timestamp   string         `json:"timestamp"`
	InsertID    string         `json:"insertId"`
}

type logMetric struct {
	Name   string `json:"name"`
	Filter string `json:"filter,omitempty"`
}

type logSink struct {
	Name        string `json:"name"`
	Destination string `json:"destination,omitempty"`
	Filter      string `json:"filter,omitempty"`
}

type state struct {
	Entries []*logEntry           `json:"entries"`
	Metrics map[string]*logMetric `json:"metrics"` // full path -> metric
	Sinks   map[string]*logSink   `json:"sinks"`   // full path -> sink
}

// Service implements the Cloud Logging emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Logging store.
func New() *Service {
	return &Service{st: state{Metrics: map[string]*logMetric{}, Sinks: map[string]*logSink{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Metrics == nil {
		s.st.Metrics = map[string]*logMetric{}
	}

	if s.st.Sinks == nil {
		s.st.Sinks = map[string]*logSink{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Logging",
		Category:    "Monitoring & Logging",
		Description: "Log entry write/list, log-based metrics, and sinks",
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

// RegisterRoutes registers the Cloud Logging REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v2/entries:write", s.writeEntries)
	r.Handle("POST", "/v2/entries:list", s.listEntries)

	metricBase := "/v2/projects/{project}/metrics"
	r.Handle("POST", metricBase, s.createMetric)
	r.Handle("GET", metricBase, s.listMetrics)
	r.Handle("GET", metricBase+"/{metric}", s.getMetric)
	r.Handle("DELETE", metricBase+"/{metric}", s.deleteMetric)

	sinkBase := "/v2/projects/{project}/sinks"
	r.Handle("POST", sinkBase, s.createSink)
	r.Handle("GET", sinkBase, s.listSinks)
	r.Handle("GET", sinkBase+"/{sink}", s.getSink)
	r.Handle("DELETE", sinkBase+"/{sink}", s.deleteSink)
}

func (s *Service) writeEntries(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Entries []*logEntry `json:"entries"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, e := range body.Entries {
		e.Timestamp = httpx.Now()
		e.InsertID = httpx.ID(8)
		s.st.Entries = append(s.st.Entries, e)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// listEntries returns all written entries. The real API's filter query
// language (resource.type=..., severity>=..., timestamp ranges) is not
// implemented — filter is accepted but ignored, matching this emulator's
// generally permissive approach to query parameters it doesn't model.
func (s *Service) listEntries(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entries": s.st.Entries})
}

func (s *Service) metricPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/metrics/"
}

func (s *Service) createMetric(w http.ResponseWriter, r *http.Request) {
	var body logMetric
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = "metric-" + httpx.ID(4)
	}

	full := s.metricPrefix(r) + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Metrics[full]; exists {
		httpx.AlreadyExists(w, "metric already exists: "+full)

		return
	}

	s.st.Metrics[full] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listMetrics(w http.ResponseWriter, r *http.Request) {
	prefix := s.metricPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Metrics {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*logMetric, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Metrics[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"metrics": items})
}

func (s *Service) getMetric(w http.ResponseWriter, r *http.Request) {
	name := s.metricPrefix(r) + r.PathValue("metric")

	s.mu.RLock()
	m, ok := s.st.Metrics[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "metric not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, m)
}

func (s *Service) deleteMetric(w http.ResponseWriter, r *http.Request) {
	name := s.metricPrefix(r) + r.PathValue("metric")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Metrics[name]; !ok {
		httpx.NotFound(w, "metric not found: "+name)

		return
	}

	delete(s.st.Metrics, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) sinkPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/sinks/"
}

func (s *Service) createSink(w http.ResponseWriter, r *http.Request) {
	var body logSink
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = "sink-" + httpx.ID(4)
	}

	full := s.sinkPrefix(r) + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Sinks[full]; exists {
		httpx.AlreadyExists(w, "sink already exists: "+full)

		return
	}

	s.st.Sinks[full] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listSinks(w http.ResponseWriter, r *http.Request) {
	prefix := s.sinkPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Sinks {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*logSink, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Sinks[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sinks": items})
}

func (s *Service) getSink(w http.ResponseWriter, r *http.Request) {
	name := s.sinkPrefix(r) + r.PathValue("sink")

	s.mu.RLock()
	sk, ok := s.st.Sinks[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "sink not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, sk)
}

func (s *Service) deleteSink(w http.ResponseWriter, r *http.Request) {
	name := s.sinkPrefix(r) + r.PathValue("sink")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Sinks[name]; !ok {
		httpx.NotFound(w, "sink not found: "+name)

		return
	}

	delete(s.st.Sinks, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
