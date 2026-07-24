// Package clouddns emulates Cloud DNS (dns.googleapis.com/dns/v1): managed
// zones and their record sets.
package clouddns

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "clouddns"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type recordSet struct {
	Name    string   `json:"name"`
	Type    string   `json:"type"`
	TTL     int      `json:"ttl,omitempty"`
	Rrdatas []string `json:"rrdatas,omitempty"`
}

type managedZone struct {
	Name       string                `json:"name"`
	DNSName    string                `json:"dnsName,omitempty"`
	RecordSets map[string]*recordSet `json:"recordSets"` // "name|type" -> record
}

type state struct {
	Zones map[string]*managedZone `json:"zones"` // projects/{p}/managedZones/{z} -> zone
}

// Service implements the Cloud DNS emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud DNS store.
func New() *Service { return &Service{st: state{Zones: map[string]*managedZone{}}} }

func (s *Service) ensureMaps() {
	if s.st.Zones == nil {
		s.st.Zones = map[string]*managedZone{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud DNS",
		Category:    "Networking",
		Description: "Managed authoritative DNS zones and record sets",
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

// RegisterRoutes registers the Cloud DNS REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/dns/v1/projects/{project}/managedZones"
	r.Handle("POST", base, s.createZone)
	r.Handle("GET", base, s.listZones)
	r.Handle("GET", base+"/{zone}", s.getZone)
	r.Handle("DELETE", base+"/{zone}", s.deleteZone)

	rrBase := base + "/{zone}/rrsets"
	r.Handle("POST", rrBase, s.createRecord)
	r.Handle("GET", rrBase, s.listRecords)
	r.Handle("DELETE", rrBase+"/{name}/{type}", s.deleteRecord)
}

func (s *Service) zonePrefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/managedZones/"
}

func (s *Service) createZone(w http.ResponseWriter, r *http.Request) {
	var body managedZone
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	full := s.zonePrefix(r) + body.Name
	body.RecordSets = map[string]*recordSet{}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Zones[full]; exists {
		httpx.AlreadyExists(w, "managed zone already exists: "+full)

		return
	}

	s.st.Zones[full] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listZones(w http.ResponseWriter, r *http.Request) {
	prefix := s.zonePrefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Zones {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*managedZone, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Zones[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"managedZones": items})
}

func (s *Service) getZone(w http.ResponseWriter, r *http.Request) {
	name := s.zonePrefix(r) + r.PathValue("zone")

	s.mu.RLock()
	z, ok := s.st.Zones[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "managed zone not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, z)
}

func (s *Service) deleteZone(w http.ResponseWriter, r *http.Request) {
	name := s.zonePrefix(r) + r.PathValue("zone")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Zones[name]; !ok {
		httpx.NotFound(w, "managed zone not found: "+name)

		return
	}

	delete(s.st.Zones, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createRecord(w http.ResponseWriter, r *http.Request) {
	var body recordSet
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if body.Name == "" || body.Type == "" {
		httpx.BadRequest(w, "name and type are required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	z, ok := s.st.Zones[s.zonePrefix(r)+r.PathValue("zone")]
	if !ok {
		httpx.NotFound(w, "managed zone not found")

		return
	}

	key := body.Name + "|" + body.Type
	if _, exists := z.RecordSets[key]; exists {
		httpx.AlreadyExists(w, "record set already exists: "+key)

		return
	}

	z.RecordSets[key] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) listRecords(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	z, ok := s.st.Zones[s.zonePrefix(r)+r.PathValue("zone")]
	if !ok {
		httpx.NotFound(w, "managed zone not found")

		return
	}

	keys := make([]string, 0, len(z.RecordSets))
	for k := range z.RecordSets {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	items := make([]*recordSet, 0, len(keys))
	for _, k := range keys {
		items = append(items, z.RecordSets[k])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"rrsets": items})
}

func (s *Service) deleteRecord(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	z, ok := s.st.Zones[s.zonePrefix(r)+r.PathValue("zone")]
	if !ok {
		httpx.NotFound(w, "managed zone not found")

		return
	}

	key := r.PathValue("name") + "|" + r.PathValue("type")
	if _, ok := z.RecordSets[key]; !ok {
		httpx.NotFound(w, "record set not found: "+key)

		return
	}

	delete(z.RecordSets, key)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
