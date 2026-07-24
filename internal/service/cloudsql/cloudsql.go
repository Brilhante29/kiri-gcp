// Package cloudsql emulates Cloud SQL (sqladmin.googleapis.com/sql/v1beta4):
// instances with start/stop lifecycle, plus nested databases and users.
package cloudsql

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "cloudsql"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type database struct {
	Name    string `json:"name"`
	Charset string `json:"charset,omitempty"`
}

type user struct {
	Name string `json:"name"`
	Host string `json:"host,omitempty"`
}

type sqlInstance struct {
	Name            string               `json:"name"`
	DatabaseVersion string               `json:"databaseVersion,omitempty"`
	State           string               `json:"state"`
	Databases       map[string]*database `json:"databases"`
	Users           map[string]*user     `json:"users"`
}

type state struct {
	Instances map[string]*sqlInstance `json:"instances"` // projects/{p}/instances/{i} -> instance
}

// Service implements the Cloud SQL emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud SQL store.
func New() *Service { return &Service{st: state{Instances: map[string]*sqlInstance{}}} }

func (s *Service) ensureMaps() {
	if s.st.Instances == nil {
		s.st.Instances = map[string]*sqlInstance{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud SQL",
		Category:    "Databases",
		Description: "Managed MySQL/PostgreSQL instances, databases, and users",
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

// RegisterRoutes registers the Cloud SQL REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/sql/v1beta4/projects/{project}/instances"
	r.Handle("POST", base, s.createInstance)
	r.Handle("GET", base, s.listInstances)
	r.Handle("GET", base+"/{inst}", s.getInstance)
	r.Handle("DELETE", base+"/{inst}", s.deleteInstance)
	// Cloud SQL has no real start/stop custom method (production uses a
	// PATCH of settings.activationPolicy); these convenience actions use
	// plain path segments rather than a ":verb" suffix.
	r.Handle("POST", base+"/{inst}/start", s.startInstance)
	r.Handle("POST", base+"/{inst}/stop", s.stopInstance)
	r.Handle("POST", base+"/{inst}/restart", s.startInstance)

	dbBase := base + "/{inst}/databases"
	r.Handle("POST", dbBase, s.createDatabase)
	r.Handle("GET", dbBase, s.listDatabases)
	r.Handle("GET", dbBase+"/{db}", s.getDatabase)
	r.Handle("DELETE", dbBase+"/{db}", s.deleteDatabase)

	userBase := base + "/{inst}/users"
	r.Handle("POST", userBase, s.createUser)
	r.Handle("GET", userBase, s.listUsers)
	r.Handle("DELETE", userBase+"/{user}", s.deleteUser)
}

func (s *Service) instName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/instances/" + r.PathValue("inst")
}

// ---- Instances ----

func (s *Service) createInstance(w http.ResponseWriter, r *http.Request) {
	var body sqlInstance
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/instances/" + body.Name
	body.State = "RUNNABLE"
	body.Databases = map[string]*database{}
	body.Users = map[string]*user{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Instances[name]; exists {
		httpx.AlreadyExists(w, "instance already exists: "+name)

		return
	}

	s.st.Instances[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listInstances(w http.ResponseWriter, r *http.Request) {
	prefix := "projects/" + r.PathValue("project") + "/instances/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Instances {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*sqlInstance, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Instances[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getInstance(w http.ResponseWriter, r *http.Request) {
	name := s.instName(r)

	s.mu.RLock()
	inst, ok := s.st.Instances[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) startInstance(w http.ResponseWriter, r *http.Request) {
	s.setInstanceState(w, r, "RUNNABLE")
}

func (s *Service) stopInstance(w http.ResponseWriter, r *http.Request) {
	s.setInstanceState(w, r, "STOPPED")
}

func (s *Service) setInstanceState(w http.ResponseWriter, r *http.Request, newState string) {
	name := s.instName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[name]
	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	inst.State = newState

	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) deleteInstance(w http.ResponseWriter, r *http.Request) {
	name := s.instName(r)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Instances[name]; !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	delete(s.st.Instances, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Databases ----

func (s *Service) createDatabase(w http.ResponseWriter, r *http.Request) {
	var body database
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, exists := inst.Databases[body.Name]; exists {
		httpx.AlreadyExists(w, "database already exists: "+body.Name)

		return
	}

	inst.Databases[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listDatabases(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	names := make([]string, 0, len(inst.Databases))
	for n := range inst.Databases {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*database, 0, len(names))
	for _, n := range names {
		items = append(items, inst.Databases[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getDatabase(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	db, ok := inst.Databases[r.PathValue("db")]
	if !ok {
		httpx.NotFound(w, "database not found: "+r.PathValue("db"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, db)
}

func (s *Service) deleteDatabase(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, ok := inst.Databases[r.PathValue("db")]; !ok {
		httpx.NotFound(w, "database not found: "+r.PathValue("db"))

		return
	}

	delete(inst.Databases, r.PathValue("db"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Users ----

func (s *Service) createUser(w http.ResponseWriter, r *http.Request) {
	var body user
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, exists := inst.Users[body.Name]; exists {
		httpx.AlreadyExists(w, "user already exists: "+body.Name)

		return
	}

	inst.Users[body.Name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listUsers(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	names := make([]string, 0, len(inst.Users))
	for n := range inst.Users {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*user, 0, len(names))
	for _, n := range names {
		items = append(items, inst.Users[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) deleteUser(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, ok := inst.Users[r.PathValue("user")]; !ok {
		httpx.NotFound(w, "user not found: "+r.PathValue("user"))

		return
	}

	delete(inst.Users, r.PathValue("user"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
