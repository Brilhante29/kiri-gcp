// Package eventarc emulates Eventarc (eventarc.googleapis.com/v1): event
// triggers and channels.
package eventarc

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "eventarc"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type trigger struct {
	Name            string           `json:"name"`
	UID             string           `json:"uid"`
	EventFilters    []map[string]any `json:"eventFilters,omitempty"`
	Destination     map[string]any   `json:"destination,omitempty"`
}

type channel struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
	State string `json:"state"`
}

type state struct {
	Triggers map[string]*trigger `json:"triggers"`
	Channels map[string]*channel `json:"channels"`
}

// Service implements the Eventarc emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Eventarc store.
func New() *Service {
	return &Service{st: state{Triggers: map[string]*trigger{}, Channels: map[string]*channel{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Triggers == nil {
		s.st.Triggers = map[string]*trigger{}
	}

	if s.st.Channels == nil {
		s.st.Channels = map[string]*channel{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Eventarc",
		Category:    "Application Integration",
		Description: "Event-driven triggers and channels from Google Cloud sources",
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

// RegisterRoutes registers the Eventarc REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	trigBase := "/v1/projects/{project}/locations/{location}/triggers"
	r.Handle("POST", trigBase, s.createTrigger)
	r.Handle("GET", trigBase, s.listTriggers)
	r.Handle("GET", trigBase+"/{trigger}", s.getTrigger)
	r.Handle("DELETE", trigBase+"/{trigger}", s.deleteTrigger)

	chBase := "/v1/projects/{project}/locations/{location}/channels"
	r.Handle("POST", chBase, s.createChannel)
	r.Handle("GET", chBase, s.listChannels)
	r.Handle("GET", chBase+"/{channel}", s.getChannel)
	r.Handle("DELETE", chBase+"/{channel}", s.deleteChannel)
}

func (s *Service) prefix(r *http.Request, kind string) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/" + kind + "/"
}

// ---- Triggers ----

func (s *Service) createTrigger(w http.ResponseWriter, r *http.Request) {
	triggerID := r.URL.Query().Get("triggerId")

	var body trigger
	_ = httpx.DecodeJSON(r, &body)

	if triggerID == "" {
		triggerID = body.Name
	}

	if triggerID == "" {
		triggerID = "trigger-" + httpx.ID(4)
	}

	name := s.prefix(r, "triggers") + triggerID
	body.Name = name
	body.UID = httpx.ID(8)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Triggers[name]; exists {
		httpx.AlreadyExists(w, "trigger already exists: "+name)

		return
	}

	s.st.Triggers[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listTriggers(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "triggers")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Triggers {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*trigger, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Triggers[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"triggers": items})
}

func (s *Service) getTrigger(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "triggers") + r.PathValue("trigger")

	s.mu.RLock()
	t, ok := s.st.Triggers[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "trigger not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTrigger(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "triggers") + r.PathValue("trigger")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Triggers[name]; !ok {
		httpx.NotFound(w, "trigger not found: "+name)

		return
	}

	delete(s.st.Triggers, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Channels ----

func (s *Service) createChannel(w http.ResponseWriter, r *http.Request) {
	channelID := r.URL.Query().Get("channelId")

	var body channel
	_ = httpx.DecodeJSON(r, &body)

	if channelID == "" {
		channelID = body.Name
	}

	if channelID == "" {
		channelID = "channel-" + httpx.ID(4)
	}

	name := s.prefix(r, "channels") + channelID
	body.Name = name
	body.UID = httpx.ID(8)
	body.State = "ACTIVE"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Channels[name]; exists {
		httpx.AlreadyExists(w, "channel already exists: "+name)

		return
	}

	s.st.Channels[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listChannels(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r, "channels")

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Channels {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*channel, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Channels[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"channels": items})
}

func (s *Service) getChannel(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "channels") + r.PathValue("channel")

	s.mu.RLock()
	c, ok := s.st.Channels[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "channel not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) deleteChannel(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r, "channels") + r.PathValue("channel")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Channels[name]; !ok {
		httpx.NotFound(w, "channel not found: "+name)

		return
	}

	delete(s.st.Channels, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
