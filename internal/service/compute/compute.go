// Package compute emulates Compute Engine (compute.googleapis.com/compute/v1):
// instances (with start/stop/reset lifecycle actions), persistent disks, and
// global firewall rules.
package compute

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "compute"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type instance struct {
	Name              string           `json:"name"`
	MachineType       string           `json:"machineType,omitempty"`
	Status            string           `json:"status"`
	Zone              string           `json:"zone,omitempty"`
	NetworkInterfaces []map[string]any `json:"networkInterfaces,omitempty"`
	Disks             []map[string]any `json:"disks,omitempty"`
}

type disk struct {
	Name   string `json:"name"`
	SizeGb any    `json:"sizeGb,omitempty"` // real API sends an integer; keep flexible
	Status string `json:"status"`
	Zone   string `json:"zone,omitempty"`
}

type firewall struct {
	Name    string           `json:"name"`
	Network string           `json:"network,omitempty"`
	Allowed []map[string]any `json:"allowed,omitempty"`
	Denied  []map[string]any `json:"denied,omitempty"`
}

type state struct {
	Instances map[string]*instance `json:"instances"` // zone-qualified full path -> instance
	Disks     map[string]*disk     `json:"disks"`
	Firewalls map[string]*firewall `json:"firewalls"`
}

// Service implements the Compute Engine emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Compute Engine store.
func New() *Service {
	return &Service{st: state{
		Instances: map[string]*instance{},
		Disks:     map[string]*disk{},
		Firewalls: map[string]*firewall{},
	}}
}

func (s *Service) ensureMaps() {
	if s.st.Instances == nil {
		s.st.Instances = map[string]*instance{}
	}

	if s.st.Disks == nil {
		s.st.Disks = map[string]*disk{}
	}

	if s.st.Firewalls == nil {
		s.st.Firewalls = map[string]*firewall{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Compute Engine",
		Category:    "Compute",
		Description: "Virtual machine instances, persistent disks, and firewall rules",
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

// RegisterRoutes registers the Compute Engine REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	instBase := "/compute/v1/projects/{project}/zones/{zone}/instances"
	r.Handle("POST", instBase, s.createInstance)
	r.Handle("GET", instBase, s.listInstances)
	// {instance} also matches "name:start" / "name:stop" / "name:reset".
	r.Handle("GET", instBase+"/{instance}", s.getInstance)
	r.Handle("POST", instBase+"/{instance}", s.instanceAction)
	r.Handle("DELETE", instBase+"/{instance}", s.deleteInstance)

	diskBase := "/compute/v1/projects/{project}/zones/{zone}/disks"
	r.Handle("POST", diskBase, s.createDisk)
	r.Handle("GET", diskBase, s.listDisks)
	r.Handle("GET", diskBase+"/{disk}", s.getDisk)
	r.Handle("DELETE", diskBase+"/{disk}", s.deleteDisk)

	fwBase := "/compute/v1/projects/{project}/global/firewalls"
	r.Handle("POST", fwBase, s.createFirewall)
	r.Handle("GET", fwBase, s.listFirewalls)
	r.Handle("GET", fwBase+"/{firewall}", s.getFirewall)
	r.Handle("DELETE", fwBase+"/{firewall}", s.deleteFirewall)
}

func zonePrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/zones/" + r.PathValue("zone")
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

	name := zonePrefix(r) + "/instances/" + body.Name
	body.Zone = r.PathValue("zone")
	body.Status = "RUNNING"

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
	prefix := zonePrefix(r) + "/instances/"

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

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getInstance(w http.ResponseWriter, r *http.Request) {
	name := zonePrefix(r) + "/instances/" + r.PathValue("instance")

	s.mu.RLock()
	inst, ok := s.st.Instances[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) instanceAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("instance"))
	name := zonePrefix(r) + "/instances/" + id

	s.mu.Lock()
	defer s.mu.Unlock()

	inst, ok := s.st.Instances[name]
	if !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	switch verb {
	case "start":
		inst.Status = "RUNNING"
	case "stop":
		inst.Status = "TERMINATED"
	case "reset":
		inst.Status = "RUNNING"
	default:
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, inst)
}

func (s *Service) deleteInstance(w http.ResponseWriter, r *http.Request) {
	name := zonePrefix(r) + "/instances/" + r.PathValue("instance")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Instances[name]; !ok {
		httpx.NotFound(w, "instance not found: "+name)

		return
	}

	delete(s.st.Instances, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Disks ----

func (s *Service) createDisk(w http.ResponseWriter, r *http.Request) {
	var body disk
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := zonePrefix(r) + "/disks/" + body.Name
	body.Zone = r.PathValue("zone")
	body.Status = "READY"

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Disks[name]; exists {
		httpx.AlreadyExists(w, "disk already exists: "+name)

		return
	}

	s.st.Disks[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listDisks(w http.ResponseWriter, r *http.Request) {
	prefix := zonePrefix(r) + "/disks/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Disks {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*disk, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Disks[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getDisk(w http.ResponseWriter, r *http.Request) {
	name := zonePrefix(r) + "/disks/" + r.PathValue("disk")

	s.mu.RLock()
	d, ok := s.st.Disks[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "disk not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, d)
}

func (s *Service) deleteDisk(w http.ResponseWriter, r *http.Request) {
	name := zonePrefix(r) + "/disks/" + r.PathValue("disk")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Disks[name]; !ok {
		httpx.NotFound(w, "disk not found: "+name)

		return
	}

	delete(s.st.Disks, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Firewalls ----

func (s *Service) createFirewall(w http.ResponseWriter, r *http.Request) {
	var body firewall
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/global/firewalls/" + body.Name

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Firewalls[name]; exists {
		httpx.AlreadyExists(w, "firewall already exists: "+name)

		return
	}

	s.st.Firewalls[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listFirewalls(w http.ResponseWriter, r *http.Request) {
	prefix := "projects/" + r.PathValue("project") + "/global/firewalls/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Firewalls {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*firewall, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Firewalls[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getFirewall(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/global/firewalls/" + r.PathValue("firewall")

	s.mu.RLock()
	fw, ok := s.st.Firewalls[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "firewall not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, fw)
}

func (s *Service) deleteFirewall(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/global/firewalls/" + r.PathValue("firewall")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Firewalls[name]; !ok {
		httpx.NotFound(w, "firewall not found: "+name)

		return
	}

	delete(s.st.Firewalls, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
