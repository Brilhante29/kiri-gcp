// Package fcm emulates Firebase Cloud Messaging (fcm.googleapis.com/v1):
// message send, plus a /kiri/fcm/sent-messages inspection
// endpoint so tests can assert on what was actually pushed without a real device.
package fcm

import (
	"net/http"
	"sort"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "fcm"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	service.Register(svc)
}

type sentMessage struct {
	Name    string         `json:"name"`
	Message map[string]any `json:"message"`
	SentAt  string         `json:"sentAt"`
}

type state struct {
	Sent []*sentMessage `json:"sent"`
}

// Service implements the Firebase Cloud Messaging emulation.
type Service struct {
	mu sync.Mutex
	st state
}

// New creates an empty FCM service.
func New() *Service { return &Service{} }

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Firebase Cloud Messaging",
		Category:    "Messaging & Integration",
		Description: "Push notification send, with a /kiri/fcm/sent-messages inspection endpoint",
		Fidelity:    service.FidelityA,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the FCM REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/projects/{project}/messages:send", s.send)
	r.Handle("GET", "/kiri/fcm/sent-messages", s.listSent)
}

func (s *Service) send(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Message map[string]any `json:"message"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	name := "projects/" + r.PathValue("project") + "/messages/" + httpx.ID(8)

	s.mu.Lock()
	s.st.Sent = append(s.st.Sent, &sentMessage{Name: name, Message: body.Message, SentAt: httpx.Now()})
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name})
}

func (s *Service) listSent(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	items := make([]*sentMessage, len(s.st.Sent))
	copy(items, s.st.Sent)

	sort.Slice(items, func(i, j int) bool { return items[i].SentAt < items[j].SentAt })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"SentMessages": items})
}
