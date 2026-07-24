// Package spannersql emulates Cloud Spanner admin
// (spanner.googleapis.com/v1): instances and the databases within them.
package spannersql

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "spannersql"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type database struct {
	Name  string `json:"name"`
	State string `json:"state"`
}

type instance struct {
	Name      string               `json:"name"`
	Config    string               `json:"config,omitempty"`
	NodeCount int                  `json:"nodeCount,omitempty"`
	State     string               `json:"state"`
	Databases map[string]*database `json:"databases"`
}

type state struct {
	Instances map[string]*instance `json:"instances"` // projects/{p}/instances/{i} -> instance
}

// Service implements the Cloud Spanner admin emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Spanner store.
func New() *Service { return &Service{st: state{Instances: map[string]*instance{}}} }

func (s *Service) ensureMaps() {
	if s.st.Instances == nil {
		s.st.Instances = map[string]*instance{}
	}

	for _, i := range s.st.Instances {
		if i.Databases == nil {
			i.Databases = map[string]*database{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Spanner",
		Category:    "Databases",
		Description: "Globally distributed relational instances and databases",
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

// RegisterRoutes registers the Cloud Spanner admin REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/instances"
	r.Handle("POST", base, s.createInstance)
	r.Handle("GET", base, s.listInstances)
	r.Handle("GET", base+"/{instance}", s.getInstance)
	r.Handle("DELETE", base+"/{instance}", s.deleteInstance)

	dbBase := base + "/{instance}/databases"
	r.Handle("POST", dbBase, s.createDatabase)
	r.Handle("GET", dbBase, s.listDatabases)
	r.Handle("GET", dbBase+"/{database}", s.getDatabase)
	r.Handle("DELETE", dbBase+"/{database}", s.deleteDatabase)
}

func (s *Service) instName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/instances/" + r.PathValue("instance")
}

// ---- Instances ----

func (s *Service) createInstance(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstanceID string   `json:"instanceId"`
		Instance   instance `json:"instance"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.InstanceID == "" {
		httpx.BadRequest(w, "instanceId is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/instances/" + body.InstanceID
	inst := body.Instance
	inst.Name = name
	inst.State = "READY"
	inst.Databases = map[string]*database{}

	if inst.NodeCount == 0 {
		inst.NodeCount = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Instances[name]; exists {
		httpx.AlreadyExists(w, "instance already exists: "+name)

		return
	}

	s.st.Instances[name] = &inst

	httpx.WriteJSON(w, http.StatusOK, inst)
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

	items := make([]*instance, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Instances[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"instances": items})
}

func (s *Service) getInstance(w http.ResponseWriter, r *http.Request) {
	name := s.instName(r)

	s.mu.RLock()
	i, ok := s.st.Instances[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, i)
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
	var body struct {
		CreateStatement string `json:"createStatement"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	// createStatement looks like "CREATE DATABASE `mydb`"; extract the name
	// between backticks, falling back to a generated id.
	dbID := extractDBName(body.CreateStatement)
	if dbID == "" {
		dbID = "db-" + httpx.ID(4)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	name := s.instName(r) + "/databases/" + dbID
	if _, exists := inst.Databases[dbID]; exists {
		httpx.AlreadyExists(w, "database already exists: "+name)

		return
	}

	db := &database{Name: name, State: "READY"}
	inst.Databases[dbID] = db

	httpx.WriteJSON(w, http.StatusOK, db)
}

func extractDBName(stmt string) string {
	start := strings.IndexByte(stmt, '`')
	if start < 0 {
		return ""
	}

	end := strings.IndexByte(stmt[start+1:], '`')
	if end < 0 {
		return ""
	}

	return stmt[start+1 : start+1+end]
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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"databases": items})
}

func (s *Service) getDatabase(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	db, ok := inst.Databases[r.PathValue("database")]
	if !ok {
		httpx.NotFound(w, "database not found: "+r.PathValue("database"))

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

	if _, ok := inst.Databases[r.PathValue("database")]; !ok {
		httpx.NotFound(w, "database not found: "+r.PathValue("database"))

		return
	}

	delete(inst.Databases, r.PathValue("database"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
