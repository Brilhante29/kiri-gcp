// Package pubsub emulates Cloud Pub/Sub's REST API v1: topics, subscriptions,
// publish, pull, and acknowledge. Custom methods (":publish", ":pull",
// ":acknowledge") are dispatched via httpx.SplitVerb, matching how the real
// API appends them to the resource path.
//
// REST and gRPC share one backend (internal/grpcsvc/pubsub.MergeBackend):
// this Service owns it and exposes it via Backend() for the server to wire
// into the gRPC Publisher/Subscriber implementation, so a client using
// PUBSUB_EMULATOR_HOST (gRPC) and a client hitting this REST API see the
// exact same topics, subscriptions, and messages.
//
// Each subscription tracks its own in-flight (pulled-but-unacked) messages;
// once acknowledged a message is gone. This is a simplified single-consumer
// model — good enough for local development and CI, not a faithful multi-
// subscriber fan-out.
package pubsub

import (
	"net/http"
	"sort"

	pubsubgrpc "github.com/kiri-dev/kiri/internal/grpcsvc/pubsub"
	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/protow"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "pubsub"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", svc.backend)
	service.Register(svc)
}

// Message is a Pub/Sub message payload used by cross-service publishers (GCS
// bucket notifications, Cloud Scheduler Pub/Sub targets) via Service.Publish.
type Message struct {
	Data       string
	Attributes map[string]string
}

// Service implements the Pub/Sub REST emulation, backed by the same
// MergeBackend the gRPC layer uses.
type Service struct {
	backend *pubsubgrpc.MergeBackend
}

// New creates a Pub/Sub service with a fresh backend.
func New() *Service {
	return &Service{backend: pubsubgrpc.NewMergeBackend()}
}

// Backend returns the shared topic/subscription/message store, for the
// server to hand to the gRPC Publisher/Subscriber implementation.
func (s *Service) Backend() *pubsubgrpc.MergeBackend { return s.backend }

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Pub/Sub",
		Category:    "Messaging & Integration",
		Description: "Topics, subscriptions, publish/pull/acknowledge — REST and gRPC share one backend",
		Fidelity:    service.FidelityA,
		State:       service.StateIntegrated,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	return storage.Save(serviceName, "state", s.backend)
}

// Publish is used by cross-service publishers (GCS bucket notifications,
// Cloud Scheduler Pub/Sub targets) that already hold a fully-qualified topic
// name. It returns the assigned message IDs, or nil if the topic is unknown.
func (s *Service) Publish(topic string, msgs []Message) []string {
	if !s.backend.GetTopic(topic) {
		return nil
	}

	return s.backend.Publish(topic, toProtoMessages(msgs))
}

func toProtoMessages(msgs []Message) []*protow.PubsubMessage {
	out := make([]*protow.PubsubMessage, len(msgs))
	for i, m := range msgs {
		out[i] = &protow.PubsubMessage{Data: []byte(m.Data), Attributes: m.Attributes}
	}

	return out
}

// RegisterRoutes registers the Pub/Sub REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/projects/{project}/topics", s.createTopic)
	r.Handle("GET", "/v1/projects/{project}/topics", s.listTopics)
	r.Handle("GET", "/v1/projects/{project}/topics/{topic}", s.getTopic)
	r.Handle("DELETE", "/v1/projects/{project}/topics/{topic}", s.deleteTopic)
	// {topic} also matches "name:publish" custom-method segments.
	r.Handle("POST", "/v1/projects/{project}/topics/{topic}", s.topicAction)

	r.Handle("POST", "/v1/projects/{project}/subscriptions", s.createSubscription)
	r.Handle("GET", "/v1/projects/{project}/subscriptions", s.listSubscriptions)
	r.Handle("GET", "/v1/projects/{project}/subscriptions/{sub}", s.getSubscription)
	r.Handle("DELETE", "/v1/projects/{project}/subscriptions/{sub}", s.deleteSubscription)
	// {sub} also matches "name:pull" / "name:acknowledge" custom-method segments.
	r.Handle("POST", "/v1/projects/{project}/subscriptions/{sub}", s.subAction)
}

// ---- Topics ----

func (s *Service) createTopic(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body struct {
		TopicID string `json:"topicId"`
	}
	_ = httpx.DecodeJSON(r, &body)

	if body.TopicID == "" {
		httpx.BadRequest(w, "topicId is required")

		return
	}

	name := "projects/" + project + "/topics/" + body.TopicID

	if s.backend.GetTopic(name) {
		httpx.AlreadyExists(w, "topic already exists: "+name)

		return
	}

	s.backend.CreateTopic(name)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name})
}

func (s *Service) listTopics(w http.ResponseWriter, r *http.Request) {
	names := s.backend.ListTopics(r.PathValue("project"))
	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, map[string]any{"name": n})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"topics": items})
}

func (s *Service) getTopic(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/topics/" + r.PathValue("topic")

	if !s.backend.GetTopic(name) {
		httpx.NotFound(w, "topic not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name})
}

func (s *Service) deleteTopic(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/topics/" + r.PathValue("topic")

	if !s.backend.DeleteTopic(name) {
		httpx.NotFound(w, "topic not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) topicAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("topic"))
	if verb != "publish" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	name := "projects/" + r.PathValue("project") + "/topics/" + id

	var body struct {
		Messages []struct {
			Data       string            `json:"data"`
			Attributes map[string]string `json:"attributes"`
		} `json:"messages"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if !s.backend.GetTopic(name) {
		httpx.NotFound(w, "topic not found: "+name)

		return
	}

	msgs := make([]Message, len(body.Messages))
	for i, m := range body.Messages {
		msgs[i] = Message{Data: m.Data, Attributes: m.Attributes}
	}

	ids := s.backend.Publish(name, toProtoMessages(msgs))

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messageIds": ids})
}

// ---- Subscriptions ----

func (s *Service) createSubscription(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body struct {
		Topic          string `json:"topic"`
		SubscriptionID string `json:"subscriptionId"`
	}
	_ = httpx.DecodeJSON(r, &body)

	if body.SubscriptionID == "" || body.Topic == "" {
		httpx.BadRequest(w, "topic and subscriptionId are required")

		return
	}

	name := "projects/" + project + "/subscriptions/" + body.SubscriptionID

	if !s.backend.GetTopic(body.Topic) {
		httpx.NotFound(w, "topic not found: "+body.Topic)

		return
	}

	if _, exists := s.backend.GetSubscription(name); exists {
		httpx.AlreadyExists(w, "subscription already exists: "+name)

		return
	}

	s.backend.CreateSubscription(name, body.Topic)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name, "topic": body.Topic})
}

func (s *Service) listSubscriptions(w http.ResponseWriter, r *http.Request) {
	names := s.backend.ListSubscriptions(r.PathValue("project"))
	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		topic, _ := s.backend.GetSubscription(n)
		items = append(items, map[string]any{"name": n, "topic": topic})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"subscriptions": items})
}

func (s *Service) getSubscription(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/subscriptions/" + r.PathValue("sub")

	topic, ok := s.backend.GetSubscription(name)
	if !ok {
		httpx.NotFound(w, "subscription not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name, "topic": topic})
}

func (s *Service) deleteSubscription(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/subscriptions/" + r.PathValue("sub")

	if !s.backend.DeleteSubscription(name) {
		httpx.NotFound(w, "subscription not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) subAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("sub"))
	name := "projects/" + r.PathValue("project") + "/subscriptions/" + id

	switch verb {
	case "pull":
		s.pull(w, name)
	case "acknowledge":
		s.acknowledge(w, r, name)
	default:
		httpx.NotFound(w, "unknown method: "+verb)
	}
}

func (s *Service) pull(w http.ResponseWriter, subName string) {
	msgs, ok := s.backend.Pull(subName)
	if !ok {
		httpx.NotFound(w, "subscription not found: "+subName)

		return
	}

	received := make([]map[string]any, 0, len(msgs))

	for _, m := range msgs {
		received = append(received, map[string]any{
			"ackId": m.AckId,
			"message": map[string]any{
				"messageId":  m.Message.MessageId,
				"data":       string(m.Message.Data),
				"attributes": m.Message.Attributes,
			},
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"receivedMessages": received})
}

func (s *Service) acknowledge(w http.ResponseWriter, r *http.Request, subName string) {
	var body struct {
		AckIDs []string `json:"ackIds"`
	}
	_ = httpx.DecodeJSON(r, &body)

	if !s.backend.Acknowledge(subName, body.AckIDs) {
		httpx.NotFound(w, "subscription not found: "+subName)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}
