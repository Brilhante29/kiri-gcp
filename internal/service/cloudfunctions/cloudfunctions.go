package cloudfunctions

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "cloudfunctions"

func init() { service.Register(New()) }

type cloudFunction struct {
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Status       string            `json:"status"`
	EntryPoint   string            `json:"entryPoint,omitempty"`
	Runtime      string            `json:"runtime,omitempty"`
	Timeout      string            `json:"timeout,omitempty"`
	HttpsTrigger map[string]string `json:"httpsTrigger,omitempty"`
	UpdateTime   string            `json:"updateTime"`
}

type state struct {
	Functions map[string]*cloudFunction `json:"functions"`
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{st: state{Functions: make(map[string]*cloudFunction)}}
	_ = storage.Load(serviceName, "state", &s.st)
	if s.st.Functions == nil {
		s.st.Functions = make(map[string]*cloudFunction)
	}
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Functions",
		Category:    "Compute",
		Description: "Event-driven serverless compute platform",
		State:       service.StateBehavioral,
		Fidelity:    service.FidelityA,
	}
}

func (s *Service) Close() error { return storage.Save(serviceName, "state", s.st) }

func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/functions", s.createFn)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/functions", s.listFn)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/functions/{fn}", s.getOrCallFn)
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/functions/{fn}", s.getOrCallFn)
	r.Handle("DELETE", "/v1/projects/{project}/locations/{location}/functions/{fn}", s.deleteFn)
}

func (s *Service) createFn(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")

	var req struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		EntryPoint  string `json:"entryPoint"`
		Runtime     string `json:"runtime"`
	}
	_ = httpx.DecodeJSON(r, &req)

	fullName := req.Name
	if fullName == "" {
		fullName = fmt.Sprintf("projects/%s/locations/%s/functions/fn-%s", project, location, httpx.ID(6))
	} else if !strings.HasPrefix(fullName, "projects/") {
		fullName = fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, location, req.Name)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Functions[fullName]; exists {
		httpx.AlreadyExists(w, fmt.Sprintf("Function %s already exists", fullName))
		return
	}

	runtime := req.Runtime
	if runtime == "" {
		runtime = "nodejs20"
	}

	fn := &cloudFunction{
		Name:        fullName,
		Description: req.Description,
		Status:      "ACTIVE",
		EntryPoint:  req.EntryPoint,
		Runtime:     runtime,
		Timeout:     "60s",
		HttpsTrigger: map[string]string{
			"url": fmt.Sprintf("https://%s-%s.cloudfunctions.net/%s", location, project, httpx.ID(8)),
		},
		UpdateTime: httpx.Now(),
	}
	s.st.Functions[fullName] = fn
	httpx.WriteJSON(w, http.StatusOK, fn)
}

func (s *Service) listFn(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	prefix := fmt.Sprintf("projects/%s/locations/%s/functions/", project, location)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*cloudFunction
	for name, fn := range s.st.Functions {
		if strings.HasPrefix(name, prefix) {
			result = append(result, fn)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"functions": result,
	})
}

func (s *Service) getOrCallFn(w http.ResponseWriter, r *http.Request) {
	rawFn := r.PathValue("fn")
	fnName, verb := httpx.SplitVerb(rawFn)

	project := r.PathValue("project")
	location := r.PathValue("location")
	fullName := fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, location, fnName)

	s.mu.RLock()
	fn, exists := s.st.Functions[fullName]
	s.mu.RUnlock()

	if verb == "call" {
		if !exists {
			httpx.NotFound(w, fmt.Sprintf("Function %s not found", fullName))
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"executionId": httpx.ID(12),
			"result":      `{"status":"ok"}`,
		})
		return
	}

	if !exists {
		httpx.NotFound(w, fmt.Sprintf("Function %s not found", fullName))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, fn)
}

func (s *Service) deleteFn(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	fnName := r.PathValue("fn")
	fullName := fmt.Sprintf("projects/%s/locations/%s/functions/%s", project, location, fnName)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Functions[fullName]; !exists {
		httpx.NotFound(w, fmt.Sprintf("Function %s not found", fullName))
		return
	}

	delete(s.st.Functions, fullName)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
