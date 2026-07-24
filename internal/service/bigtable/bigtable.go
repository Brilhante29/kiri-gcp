// Package bigtable emulates Bigtable admin (bigtableadmin.googleapis.com/v2):
// instances and the tables (with column families) within them.
package bigtable

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "bigtable"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type table struct {
	Name           string                    `json:"name"`
	ColumnFamilies map[string]map[string]any `json:"columnFamilies,omitempty"`
}

type instance struct {
	Name   string            `json:"name"`
	State  string            `json:"state"`
	Tables map[string]*table `json:"tables"`
}

type state struct {
	Instances map[string]*instance `json:"instances"` // projects/{p}/instances/{i} -> instance
}

// Service implements the Bigtable admin emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Bigtable store.
func New() *Service { return &Service{st: state{Instances: map[string]*instance{}}} }

func (s *Service) ensureMaps() {
	if s.st.Instances == nil {
		s.st.Instances = map[string]*instance{}
	}

	for _, i := range s.st.Instances {
		if i.Tables == nil {
			i.Tables = map[string]*table{}
		}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Bigtable",
		Category:    "Databases",
		Description: "Wide-column NoSQL instances and tables",
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

// RegisterRoutes registers the Bigtable admin REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v2/projects/{project}/instances"
	r.Handle("POST", base, s.createInstance)
	r.Handle("GET", base, s.listInstances)
	r.Handle("GET", base+"/{instance}", s.getInstance)
	r.Handle("DELETE", base+"/{instance}", s.deleteInstance)

	tblBase := base + "/{instance}/tables"
	r.Handle("POST", tblBase, s.createTable)
	r.Handle("GET", tblBase, s.listTables)
	r.Handle("GET", tblBase+"/{table}", s.getTable)
	r.Handle("DELETE", tblBase+"/{table}", s.deleteTable)
}

func (s *Service) instName(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/instances/" + r.PathValue("instance")
}

// ---- Instances ----

func (s *Service) createInstance(w http.ResponseWriter, r *http.Request) {
	var body instance
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/instances/" + body.Name
	body.State = "READY"
	body.Tables = map[string]*table{}

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

// ---- Tables ----

func (s *Service) createTable(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TableID string `json:"tableId"`
		Table   table  `json:"table"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.TableID == "" {
		httpx.BadRequest(w, "tableId is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, exists := inst.Tables[body.TableID]; exists {
		httpx.AlreadyExists(w, "table already exists: "+body.TableID)

		return
	}

	t := body.Table
	t.Name = s.instName(r) + "/tables/" + body.TableID

	if t.ColumnFamilies == nil {
		t.ColumnFamilies = map[string]map[string]any{}
	}

	inst.Tables[body.TableID] = &t

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) listTables(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	names := make([]string, 0, len(inst.Tables))
	for n := range inst.Tables {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*table, 0, len(names))
	for _, n := range names {
		items = append(items, inst.Tables[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"tables": items})
}

func (s *Service) getTable(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	t, ok := inst.Tables[r.PathValue("table")]
	if !ok {
		httpx.NotFound(w, "table not found: "+r.PathValue("table"))

		return
	}

	httpx.WriteJSON(w, http.StatusOK, t)
}

func (s *Service) deleteTable(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[s.instName(r)]
	if !ok {
		httpx.NotFound(w, "instance not found: "+s.instName(r))

		return
	}

	if _, ok := inst.Tables[r.PathValue("table")]; !ok {
		httpx.NotFound(w, "table not found: "+r.PathValue("table"))

		return
	}

	delete(inst.Tables, r.PathValue("table"))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
