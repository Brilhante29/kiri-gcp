// Package cloudtasks emulates Cloud Tasks (cloudtasks.googleapis.com/v2):
// queues and their tasks, with a :run custom method that executes a task
// immediately (the emulator does not model queue-driven delivery delay).
package cloudtasks

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "cloudtasks"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type task struct {
	Name        string         `json:"name"`
	HTTPRequest map[string]any `json:"httpRequest,omitempty"`
	View        string         `json:"view,omitempty"`
	Dispatched  bool           `json:"dispatched"`
}

type queue struct {
	Name  string           `json:"name"`
	State string           `json:"state"`
	Tasks map[string]*task `json:"tasks"`
}

type state struct {
	Queues map[string]*queue `json:"queues"` // full path -> queue
}

// Service implements the Cloud Tasks emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Tasks store.
func New() *Service { return &Service{st: state{Queues: map[string]*queue{}}} }

func (s *Service) ensureMaps() {
	if s.st.Queues == nil {
		s.st.Queues = map[string]*queue{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Tasks",
		Category:    "Application Integration",
		Description: "Managed asynchronous task queues",
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

// RegisterRoutes registers the Cloud Tasks REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v2/projects/{project}/locations/{location}/queues"
	r.Handle("POST", base, s.createQueue)
	r.Handle("GET", base, s.listQueues)
	r.Handle("GET", base+"/{queue}", s.getQueue)
	r.Handle("DELETE", base+"/{queue}", s.deleteQueue)

	taskBase := base + "/{queue}/tasks"
	r.Handle("POST", taskBase, s.createTask)
	r.Handle("GET", taskBase, s.listTasks)
	// {task} also matches "name:run".
	r.Handle("GET", taskBase+"/{task}", s.getTask)
	r.Handle("POST", taskBase+"/{task}", s.taskAction)
	r.Handle("DELETE", taskBase+"/{task}", s.deleteTask)
}

func (s *Service) queuePrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/queues/"
}

func (s *Service) queueName(r *http.Request) string {
	return s.queuePrefix(r) + r.PathValue("queue")
}

func (s *Service) createQueue(w http.ResponseWriter, r *http.Request) {
	var body queue
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		body.Name = s.queuePrefix(r) + "queue-" + httpx.ID(4)
	}

	body.State = "RUNNING"
	body.Tasks = map[string]*task{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Queues[body.Name]; exists {
		httpx.AlreadyExists(w, "queue already exists: "+body.Name)

		return
	}

	s.st.Queues[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listQueues(w http.ResponseWriter, r *http.Request) {
	prefix := s.queuePrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Queues {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*queue, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Queues[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"queues": items})
}

func (s *Service) getQueue(w http.ResponseWriter, r *http.Request) {
	name := s.queueName(r)

	s.mu.RLock()
	q, ok := s.st.Queues[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "queue not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, q)
}

func (s *Service) deleteQueue(w http.ResponseWriter, r *http.Request) {
	name := s.queueName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Queues[name]; !ok {
		httpx.NotFound(w, "queue not found: "+name)

		return
	}

	delete(s.st.Queues, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createTask(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Task task `json:"task"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	t := body.Task

	s.mu.Lock()
	defer s.mu.Unlock()

	q, ok := s.st.Queues[s.queueName(r)]
	if !ok {
		httpx.NotFound(w, "queue not found: "+s.queueName(r))

		return
	}

	if t.Name == "" {
		t.Name = s.queueName(r) + "/tasks/" + httpx.ID(8)
	}

	q.Tasks[t.Name] = &t

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) listTasks(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q, ok := s.st.Queues[s.queueName(r)]
	if !ok {
		httpx.NotFound(w, "queue not found: "+s.queueName(r))

		return
	}

	names := make([]string, 0, len(q.Tasks))
	for n := range q.Tasks {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*task, 0, len(names))
	for _, n := range names {
		items = append(items, q.Tasks[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tasks": items})
}

func (s *Service) getTask(w http.ResponseWriter, r *http.Request) {
	name := s.queueName(r) + "/tasks/" + r.PathValue("task")

	s.mu.RLock()
	defer s.mu.RUnlock()

	q, ok := s.st.Queues[s.queueName(r)]
	if !ok {
		httpx.NotFound(w, "queue not found")

		return
	}

	t, ok := q.Tasks[name]
	if !ok {
		httpx.NotFound(w, "task not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) taskAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("task"))
	if verb != "run" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	name := s.queueName(r) + "/tasks/" + id

	s.mu.Lock()
	defer s.mu.Unlock()

	q, ok := s.st.Queues[s.queueName(r)]
	if !ok {
		httpx.NotFound(w, "queue not found")

		return
	}

	t, ok := q.Tasks[name]
	if !ok {
		httpx.NotFound(w, "task not found: "+name)

		return
	}

	t.Dispatched = true

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTask(w http.ResponseWriter, r *http.Request) {
	name := s.queueName(r) + "/tasks/" + r.PathValue("task")

	s.mu.Lock()
	defer s.mu.Unlock()

	q, ok := s.st.Queues[s.queueName(r)]
	if !ok {
		httpx.NotFound(w, "queue not found")

		return
	}

	if _, ok := q.Tasks[name]; !ok {
		httpx.NotFound(w, "task not found: "+name)

		return
	}

	delete(q.Tasks, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
