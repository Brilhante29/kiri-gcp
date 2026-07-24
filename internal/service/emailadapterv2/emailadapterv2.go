package emailadapterv2

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "emailadapterv2"

func init() { service.Register(New()) }

type state struct {
	Resources map[string]any `json:"resources"`
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{
		st: state{Resources: make(map[string]any)},
	}
	_ = storage.Load(serviceName, "state", &s.st)
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) Meta() service.Meta {
	return service.Meta{Display: "Email Adapter v2", Category: "Messaging & Integration", Description: "Outbound transactional email delivery (v2 API)", State: service.StateBehavioral, Fidelity: service.FidelityC}
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return storage.Save(serviceName, "state", s.st)
}

func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/"+s.Name(), s.listItems)
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/"+s.Name(), s.createItem)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/"+s.Name()+"/{id}", s.getItem)
	r.Handle("DELETE", "/v1/projects/{project}/locations/{location}/"+s.Name()+"/{id}", s.deleteItem)
}

// parent returns the resource collection name derived from the actual
// request path parameters (project/location), not a hardcoded placeholder.
func (s *Service) parent(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/" + s.Name()
}

func (s *Service) listItems(w http.ResponseWriter, r *http.Request) {
	prefix := s.parent(r) + "/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for name := range s.st.Resources {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		if m, ok := s.st.Resources[n].(map[string]any); ok {
			items = append(items, m)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) createItem(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body == nil {
		body = map[string]any{}
	}

	id := r.URL.Query().Get("id")
	if id == "" {
		id = httpx.ID(8)
	}

	name := s.parent(r) + "/" + id
	body["name"] = name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Resources[name]; exists {
		httpx.AlreadyExists(w, "resource already exists: "+name)

		return
	}

	s.st.Resources[name] = body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) getItem(w http.ResponseWriter, r *http.Request) {
	name := s.parent(r) + "/" + r.PathValue("id")

	s.mu.RLock()
	item, ok := s.st.Resources[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "resource not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, item)
}

func (s *Service) deleteItem(w http.ResponseWriter, r *http.Request) {
	name := s.parent(r) + "/" + r.PathValue("id")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Resources[name]; !ok {
		httpx.NotFound(w, "resource not found: "+name)

		return
	}

	delete(s.st.Resources, name)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
