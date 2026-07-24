// Package certificatemanager emulates Certificate Manager
// (certificatemanager.googleapis.com/v1): TLS certificate provisioning.
package certificatemanager

import (
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "certificatemanager"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

type certificate struct {
	Name           string   `json:"name"`
	SANDnsnames    []string `json:"sanDnsnames,omitempty"`
	PemCertificate string   `json:"pemCertificate,omitempty"`
}

type state struct {
	Certificates map[string]*certificate `json:"certificates"` // full path -> certificate
}

// Service implements the Certificate Manager emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Certificate Manager store.
func New() *Service { return &Service{st: state{Certificates: map[string]*certificate{}}} }

func (s *Service) ensureMaps() {
	if s.st.Certificates == nil {
		s.st.Certificates = map[string]*certificate{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Certificate Manager",
		Category:    "Security",
		Description: "TLS certificate provisioning and management",
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

// RegisterRoutes registers the Certificate Manager REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	base := "/v1/projects/{project}/locations/{location}/certificates"
	r.Handle("POST", base, s.create)
	r.Handle("GET", base, s.list)
	r.Handle("GET", base+"/{certificate}", s.get)
	r.Handle("DELETE", base+"/{certificate}", s.delete)
}

func (s *Service) prefix(r *http.Request) string {
	return "projects/" + r.PathValue("project") + "/locations/" + r.PathValue("location") + "/certificates/"
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	certID := r.URL.Query().Get("certificateId")

	var body certificate
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if certID == "" {
		httpx.BadRequest(w, "certificateId query parameter is required")

		return
	}

	name := s.prefix(r) + certID
	body.Name = name

	if body.PemCertificate == "" {
		body.PemCertificate = "-----BEGIN CERTIFICATE-----\nkiri-stub\n-----END CERTIFICATE-----"
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Certificates[name]; exists {
		httpx.AlreadyExists(w, "certificate already exists: "+name)

		return
	}

	s.st.Certificates[name] = &body

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := s.prefix(r)

	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0)
	for n := range s.st.Certificates {
		if strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]*certificate, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Certificates[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"certificates": items})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("certificate")

	s.mu.RLock()
	c, ok := s.st.Certificates[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "certificate not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, c)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := s.prefix(r) + r.PathValue("certificate")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Certificates[name]; !ok {
		httpx.NotFound(w, "certificate not found: "+name)

		return
	}

	delete(s.st.Certificates, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
