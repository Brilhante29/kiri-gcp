// Package networkconnectivity emulates Network Connectivity Center
// (networkconnectivity.googleapis.com/v1): hubs and the spokes attached to
// them.
package networkconnectivity

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "networkconnectivity"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type hub struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	State       string   `json:"state"`
	Spokes      []string `json:"spokes,omitempty"`
}

type spoke struct {
	Name  string `json:"name"`
	Hub   string `json:"hub"`
	State string `json:"state"`
}

type state struct {
	Hubs   map[string]*hub   `json:"hubs"`   // projects/{p}/locations/global/hubs/{h} -> hub
	Spokes map[string]*spoke `json:"spokes"` // full path -> spoke
}

// Service implements the Network Connectivity Center emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Network Connectivity Center store.
func New() *Service {
	return &Service{st: state{Hubs: map[string]*hub{}, Spokes: map[string]*spoke{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Hubs == nil {
		s.st.Hubs = map[string]*hub{}
	}

	if s.st.Spokes == nil {
		s.st.Spokes = map[string]*spoke{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Network Connectivity Center",
		Category:    "Networking",
		Description: "Hybrid and multi-cloud connectivity hubs and spokes",
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

// RegisterRoutes registers the Network Connectivity Center REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	hubBase := "/v1/projects/{project}/locations/global/hubs"
	r.Handle("POST", hubBase, s.createHub)
	r.Handle("GET", hubBase, s.listHubs)
	r.Handle("GET", hubBase+"/{hub}", s.getHub)
	r.Handle("DELETE", hubBase+"/{hub}", s.deleteHub)

	spokeBase := "/v1/projects/{project}/locations/{location}/spokes"
	r.Handle("POST", spokeBase, s.createSpoke)
	r.Handle("GET", spokeBase, s.listSpokes)
	r.Handle("GET", spokeBase+"/{spoke}", s.getSpoke)
	r.Handle("DELETE", spokeBase+"/{spoke}", s.deleteSpoke)
}

func (s *Service) hubPrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/global/hubs/"
}

func (s *Service) spokePrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/spokes/"
}

// ---- Hubs ----

func (s *Service) createHub(w http.ResponseWriter, r *http.Request) {
	var body hub
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := s.hubPrefix(r) + body.Name
	body.State = "ACTIVE"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Hubs[name]; exists {
		httpx.AlreadyExists(w, "hub already exists: "+name)

		return
	}

	s.st.Hubs[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listHubs(w http.ResponseWriter, r *http.Request) {
	prefix := s.hubPrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Hubs {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*hub, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Hubs[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"hubs": items})
}

func (s *Service) getHub(w http.ResponseWriter, r *http.Request) {
	name := s.hubPrefix(r) + r.PathValue("hub")

	s.mu.RLock()
	h, ok := s.st.Hubs[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "hub not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, h)
}

func (s *Service) deleteHub(w http.ResponseWriter, r *http.Request) {
	name := s.hubPrefix(r) + r.PathValue("hub")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Hubs[name]; !ok {
		httpx.NotFound(w, "hub not found: "+name)

		return
	}

	delete(s.st.Hubs, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Spokes ----

func (s *Service) createSpoke(w http.ResponseWriter, r *http.Request) {
	var body spoke
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" || body.Hub == "" {
		httpx.BadRequest(w, "name and hub are required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	h, ok := s.st.Hubs[body.Hub]
	if !ok {
		httpx.NotFound(w, "hub not found: "+body.Hub)

		return
	}

	name := s.spokePrefix(r) + body.Name
	body.State = "ACTIVE"

	h.Spokes = append(h.Spokes, name)
	s.st.Spokes[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listSpokes(w http.ResponseWriter, r *http.Request) {
	prefix := s.spokePrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Spokes {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*spoke, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Spokes[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"spokes": items})
}

func (s *Service) getSpoke(w http.ResponseWriter, r *http.Request) {
	name := s.spokePrefix(r) + r.PathValue("spoke")

	s.mu.RLock()
	sp, ok := s.st.Spokes[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "spoke not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, sp)
}

func (s *Service) deleteSpoke(w http.ResponseWriter, r *http.Request) {
	name := s.spokePrefix(r) + r.PathValue("spoke")

	s.mu.Lock()
	defer s.mu.Unlock()

	sp, ok := s.st.Spokes[name]
	if !ok {
		httpx.NotFound(w, "spoke not found: "+name)

		return
	}

	if h, ok := s.st.Hubs[sp.Hub]; ok {
		filtered := h.Spokes[:0]

		for _, n := range h.Spokes {
			if n != name {
				filtered = append(filtered, n)
			}
		}

		h.Spokes = filtered
	}

	delete(s.st.Spokes, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
