// Package pubsubgrpc implements the gRPC Pub/Sub service
// (google.pubsub.v1.Publisher + google.pubsub.v1.Subscriber) for the
// kiri emulator. It wraps the REST Pub/Sub service logic so that
// clients using PUBSUB_EMULATOR_HOST get drop-in compatibility.
package pubsubgrpc

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kiri-dev/kiri/internal/protow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Backend is the interface the gRPC layer calls into.
// In production it is backed by the REST Pub/Sub service; in tests a mock.
type Backend interface {
	Publish(topic string, messages []*protow.PubsubMessage) []string
	CreateTopic(name string)
	GetTopic(name string) bool
	DeleteTopic(name string) bool
	ListTopics(project string) []string
	CreateSubscription(name, topic string) bool
	GetSubscription(name string) (topic string, ok bool)
	DeleteSubscription(name string) bool
	ListSubscriptions(project string) []string
	Pull(subscription string) ([]*protow.ReceivedMessage, bool)
	Acknowledge(subscription string, ackIDs []string) bool
}

type grpcService struct {
	backend Backend
}

type publisherServer interface{}
type subscriberServer interface{}

// RegisterWith registers the Pub/Sub gRPC Publisher and Subscriber services.
func RegisterWith(b Backend) func(*grpc.Server) {
	return func(s *grpc.Server) {
		svc := &grpcService{backend: b}
		s.RegisterService(&publisherServiceDesc, svc)
		s.RegisterService(&subscriberServiceDesc, svc)
	}
}

// runUnary invokes the server-wide unary interceptor (if any) around fn,
// matching the dispatch pattern protoc-gen-go-grpc generates. The hand-rolled
// MethodDesc.Handler functions in this file previously called their backend
// logic directly and discarded the interceptor parameter entirely, which
// meant grpc.UnaryInterceptor (used by the server for request logging) never
// actually ran for any Pub/Sub RPC despite calls succeeding — confirmed via a
// real client where every call worked but zero log lines appeared server
// side. fn must not itself depend on req; requests are already decoded by
// the caller before runUnary is invoked.
func runUnary(ctx context.Context, srv any, fullMethod string, interceptor grpc.UnaryServerInterceptor, fn func() (any, error)) (any, error) {
	if interceptor == nil {
		return fn()
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: fullMethod}
	handler := func(ctx context.Context, _ any) (any, error) { return fn() }

	return interceptor(ctx, struct{}{}, info, handler)
}

// MergeBackend adapts the REST service state into the Backend interface
// by sharing access to its internal maps. This is a thin adapter.
type MergeBackend struct {
	mu            sync.RWMutex
	Topics        map[string]bool
	Subscriptions map[string]string // subName -> topicName
	Messages      map[string][]*protow.PubsubMessage
	Acked         map[string]map[string]bool // subName -> msgID -> acked
}

func NewMergeBackend() *MergeBackend {
	return &MergeBackend{
		Topics:        map[string]bool{},
		Subscriptions: map[string]string{},
		Messages:      map[string][]*protow.PubsubMessage{},
		Acked:         map[string]map[string]bool{},
	}
}

func (b *MergeBackend) Publish(topic string, messages []*protow.PubsubMessage) []string {
	b.mu.Lock()
	defer b.mu.Unlock()

	ids := make([]string, len(messages))
	for i, m := range messages {
		m.MessageId = protow.ID(16)
		ids[i] = m.MessageId
	}
	b.Messages[topic] = append(b.Messages[topic], messages...)
	return ids
}

func (b *MergeBackend) CreateTopic(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Topics[name] = true
	if b.Messages[name] == nil {
		b.Messages[name] = []*protow.PubsubMessage{}
	}
}

func (b *MergeBackend) GetTopic(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.Topics[name]
}

func (b *MergeBackend) DeleteTopic(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.Topics[name] {
		return false
	}
	delete(b.Topics, name)
	delete(b.Messages, name)
	return true
}

func (b *MergeBackend) ListTopics(project string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	prefix := "projects/" + project + "/topics/"
	var out []string
	for name := range b.Topics {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, name)
		}
	}
	return out
}

func (b *MergeBackend) CreateSubscription(name, topic string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Subscriptions[name] = topic
	if b.Acked[name] == nil {
		b.Acked[name] = map[string]bool{}
	}
	return true
}

func (b *MergeBackend) GetSubscription(name string) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	topic, ok := b.Subscriptions[name]
	return topic, ok
}

func (b *MergeBackend) DeleteSubscription(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.Subscriptions[name]; !ok {
		return false
	}
	delete(b.Subscriptions, name)
	return true
}

func (b *MergeBackend) ListSubscriptions(project string) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	prefix := "projects/" + project + "/subscriptions/"
	var out []string
	for name := range b.Subscriptions {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, name)
		}
	}
	return out
}

func (b *MergeBackend) Pull(subscription string) ([]*protow.ReceivedMessage, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	topic, ok := b.Subscriptions[subscription]
	if !ok {
		return nil, false
	}

	msgs := b.Messages[topic]
	acked := b.Acked[subscription]

	var received []*protow.ReceivedMessage
	var remaining []*protow.PubsubMessage

	for _, m := range msgs {
		if acked[m.MessageId] {
			remaining = append(remaining, m)
			continue
		}
		received = append(received, &protow.ReceivedMessage{
			AckId:   m.MessageId + "-ack",
			Message: m,
		})
		acked[m.MessageId] = true
	}

	// Keep unacked + acked messages that haven't been pulled yet
	b.Messages[topic] = remaining
	// Re-add the received ones so acknowledge can find them
	b.Messages[topic] = append(b.Messages[topic], msgsFromReceived(received)...)

	return received, true
}

func (b *MergeBackend) Acknowledge(subscription string, ackIDs []string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	topic, ok := b.Subscriptions[subscription]
	if !ok {
		return false
	}

	ackSet := map[string]bool{}
	for _, id := range ackIDs {
		msgID := id
		if len(id) > 4 && id[len(id)-4:] == "-ack" {
			msgID = id[:len(id)-4]
		}
		ackSet[msgID] = true
	}

	// Remove acked messages from topic store.
	var remaining []*protow.PubsubMessage
	for _, m := range b.Messages[topic] {
		if !ackSet[m.MessageId] {
			remaining = append(remaining, m)
		}
	}
	b.Messages[topic] = remaining

	return true
}

func msgsFromReceived(received []*protow.ReceivedMessage) []*protow.PubsubMessage {
	out := make([]*protow.PubsubMessage, len(received))
	for i, r := range received {
		out[i] = r.Message
	}
	return out
}

// --- gRPC Publisher service ---

var publisherServiceDesc = grpc.ServiceDesc{
	ServiceName: "google.pubsub.v1.Publisher",
	HandlerType: (*publisherServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "Publish", Handler: _Publisher_Publish_Handler},
		{MethodName: "CreateTopic", Handler: _Publisher_CreateTopic_Handler},
		{MethodName: "GetTopic", Handler: _Publisher_GetTopic_Handler},
		{MethodName: "DeleteTopic", Handler: _Publisher_DeleteTopic_Handler},
		{MethodName: "ListTopics", Handler: _Publisher_ListTopics_Handler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "google/pubsub/v1/pubsub.proto",
}

func _Publisher_Publish_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	pb := protow.DecodePublishRequest(raw)
	svc := srv.(*grpcService)
	dbgf("Publish: topic=%q nMessages=%d", pb.Topic, len(pb.Messages))

	return runUnary(ctx, srv, "/google.pubsub.v1.Publisher/Publish", interceptor, func() (any, error) {
		ids := svc.backend.Publish(pb.Topic, pb.Messages)
		dbgf("Publish: assigned ids=%v", ids)

		return &protow.PublishResponse{MessageIds: ids}, nil
	})
}

func _Publisher_CreateTopic_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeStringMsg(raw)
	svc := srv.(*grpcService)
	dbgf("CreateTopic: name=%q", req.Field1)

	return runUnary(ctx, srv, "/google.pubsub.v1.Publisher/CreateTopic", interceptor, func() (any, error) {
		svc.backend.CreateTopic(req.Field1)

		return &protow.TopicMsg{Name: req.Field1}, nil
	})
}

func _Publisher_GetTopic_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeStringMsg(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Publisher/GetTopic", interceptor, func() (any, error) {
		if !svc.backend.GetTopic(req.Field1) {
			return nil, status.Errorf(codes.NotFound, "topic not found: %s", req.Field1)
		}

		return &protow.TopicMsg{Name: req.Field1}, nil
	})
}

func _Publisher_DeleteTopic_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeStringMsg(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Publisher/DeleteTopic", interceptor, func() (any, error) {
		if !svc.backend.DeleteTopic(req.Field1) {
			return nil, status.Errorf(codes.NotFound, "topic not found: %s", req.Field1)
		}

		return &protow.Empty{}, nil
	})
}

func _Publisher_ListTopics_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	// ListTopicsRequest: field 1 = parent (string)
	d := protow.NewDecoder(raw)
	parent, _ := d.ScanString(1)

	svc := srv.(*grpcService)
	// Extract project from parent path "projects/{project}"
	var project string
	if len(parent) > 9 && parent[:9] == "projects/" {
		parts := split2(parent[9:], "/")
		project = parts[0]
	}

	return runUnary(ctx, srv, "/google.pubsub.v1.Publisher/ListTopics", interceptor, func() (any, error) {
		names := svc.backend.ListTopics(project)

		topics := make([]*protow.TopicMsg, 0, len(names))
		for _, n := range names {
			topics = append(topics, &protow.TopicMsg{Name: n})
		}

		return &protow.ListTopicsResponse{Topics: topics}, nil
	})
}

// --- gRPC Subscriber service ---

var subscriberServiceDesc = grpc.ServiceDesc{
	ServiceName: "google.pubsub.v1.Subscriber",
	HandlerType: (*subscriberServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "CreateSubscription", Handler: _Subscriber_CreateSubscription_Handler},
		{MethodName: "GetSubscription", Handler: _Subscriber_GetSubscription_Handler},
		{MethodName: "DeleteSubscription", Handler: _Subscriber_DeleteSubscription_Handler},
		{MethodName: "ListSubscriptions", Handler: _Subscriber_ListSubscriptions_Handler},
		{MethodName: "Pull", Handler: _Subscriber_Pull_Handler},
		{MethodName: "Acknowledge", Handler: _Subscriber_Acknowledge_Handler},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "StreamingPull",
			Handler:       _Subscriber_StreamingPull_Handler,
			ServerStreams: true,
			ClientStreams: true,
		},
	},
	Metadata: "google/pubsub/v1/pubsub.proto",
}

func _Subscriber_CreateSubscription_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	// Subscription proto: field 1 = name, field 2 = topic. Scanned via one
	// pass over d.Next() (not two sequential ScanString calls) because
	// ScanString consumes the decoder position as it walks — two sequential
	// calls only find both fields if the client happens to encode them in
	// ascending field-number order, which protobuf does not guarantee.
	d := protow.NewDecoder(raw)

	var name, topic string

	for {
		num, typ, val, ok := d.Next()
		if !ok {
			break
		}

		switch num {
		case 1:
			name = string(val)
		case 2:
			topic = string(val)
		}

		_ = typ
	}

	svc := srv.(*grpcService)
	dbgf("CreateSubscription: name=%q topic=%q (raw len=%d)", name, topic, len(raw))

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/CreateSubscription", interceptor, func() (any, error) {
		svc.backend.CreateSubscription(name, topic)

		return &protow.SubscriptionMsg{Name: name, Topic: topic}, nil
	})
}

func _Subscriber_GetSubscription_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeStringMsg(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/GetSubscription", interceptor, func() (any, error) {
		topic, ok := svc.backend.GetSubscription(req.Field1)
		if !ok {
			return nil, status.Errorf(codes.NotFound, "subscription not found: %s", req.Field1)
		}

		return &protow.SubscriptionMsg{Name: req.Field1, Topic: topic}, nil
	})
}

func _Subscriber_DeleteSubscription_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeStringMsg(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/DeleteSubscription", interceptor, func() (any, error) {
		if !svc.backend.DeleteSubscription(req.Field1) {
			return nil, status.Errorf(codes.NotFound, "subscription not found: %s", req.Field1)
		}

		return &protow.Empty{}, nil
	})
}

func _Subscriber_ListSubscriptions_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	d := protow.NewDecoder(raw)
	parent, _ := d.ScanString(1)

	svc := srv.(*grpcService)
	var project string
	if len(parent) > 9 && parent[:9] == "projects/" {
		parts := split2(parent[9:], "/")
		project = parts[0]
	}

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/ListSubscriptions", interceptor, func() (any, error) {
		names := svc.backend.ListSubscriptions(project)

		subs := make([]*protow.SubscriptionMsg, 0, len(names))
		for _, n := range names {
			subs = append(subs, &protow.SubscriptionMsg{Name: n})
		}

		return &protow.ListSubscriptionsResponse{Subscriptions: subs}, nil
	})
}

func _Subscriber_Pull_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodePullRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/Pull", interceptor, func() (any, error) {
		msgs, ok := svc.backend.Pull(req.Subscription)
		if !ok {
			return nil, status.Errorf(codes.NotFound, "subscription not found: %s", req.Subscription)
		}

		return &protow.PullResponse{Messages: msgs}, nil
	})
}

func _Subscriber_Acknowledge_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeAcknowledgeRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.pubsub.v1.Subscriber/Acknowledge", interceptor, func() (any, error) {
		if !svc.backend.Acknowledge(req.Subscription, req.AckIds) {
			return nil, status.Errorf(codes.NotFound, "subscription not found: %s", req.Subscription)
		}

		return &protow.Empty{}, nil
	})
}

// debugStreamingPull enables verbose stderr tracing of the StreamingPull
// handler when KIRI_DEBUG_STREAMINGPULL is set. Kept behind an env var
// rather than deleted outright: bidi-streaming has no request/response log
// hook like runUnary's interceptor does, so this is the only way to observe
// its behavior against a real client without attaching a debugger.
var debugStreamingPull = os.Getenv("KIRI_DEBUG_STREAMINGPULL") != ""

func dbgf(format string, args ...any) {
	if debugStreamingPull {
		fmt.Fprintf(os.Stderr, "[streamingpull] "+format+"\n", args...)
	}
}

// _Subscriber_StreamingPull_Handler implements the bidi-streaming RPC the
// real Go Pub/Sub client's Subscription.Receive() actually uses (not the
// unary Pull above) — confirmed against cloud.google.com/go/pubsub, which
// fails with "Unimplemented desc = unknown method StreamingPull" without
// this. A background goroutine drains client requests (the subscription
// name on the first message, ack ids on any message) while the main loop
// polls the backend and pushes newly-queued messages on a short interval.
func _Subscriber_StreamingPull_Handler(srv any, stream grpc.ServerStream) error {
	svc := srv.(*grpcService)
	dbgf("handler started")

	type recvResult struct {
		req *protow.StreamingPullRequest
		err error
	}

	recvCh := make(chan recvResult)

	go func() {
		for {
			var raw []byte
			if err := stream.RecvMsg(&raw); err != nil {
				dbgf("recv error (stream closing): %v", err)
				recvCh <- recvResult{nil, err}

				return
			}

			req := protow.DecodeStreamingPullRequest(raw)
			dbgf("recv request: subscription=%q ackIds=%v (raw len=%d)", req.Subscription, req.AckIds, len(raw))
			recvCh <- recvResult{req, nil}
		}
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	var subscription string

	for {
		select {
		case <-stream.Context().Done():
			dbgf("stream context done: %v", stream.Context().Err())

			return stream.Context().Err()

		case res := <-recvCh:
			if res.err != nil {
				// Client closed the send side (e.g. sub.Receive's context was
				// cancelled) — this is normal stream termination, not a fault.
				return nil
			}

			if res.req.Subscription != "" {
				subscription = res.req.Subscription
				dbgf("subscription set to %q", subscription)
			}

			if len(res.req.AckIds) > 0 && subscription != "" {
				svc.backend.Acknowledge(subscription, res.req.AckIds)
				dbgf("acknowledged %d ids for %q", len(res.req.AckIds), subscription)
			}

		case <-ticker.C:
			if subscription == "" {
				continue
			}

			msgs, ok := svc.backend.Pull(subscription)
			dbgf("poll subscription=%q backendOk=%v nMsgs=%d", subscription, ok, len(msgs))

			if !ok || len(msgs) == 0 {
				continue
			}

			resp := &protow.PullResponse{Messages: msgs}
			encoded := resp.Encode()
			dbgf("sending response: %d messages, %d encoded bytes", len(msgs), len(encoded))

			if err := stream.SendMsg(encoded); err != nil {
				dbgf("send error: %v", err)

				return err
			}

			dbgf("send OK")
		}
	}
}

// --- helpers ---

func split2(s, sep string) [2]string {
	idx := -1
	for i := 0; i < len(s)-len(sep)+1; i++ {
		if s[i:i+len(sep)] == sep {
			idx = i
			break
		}
	}
	if idx < 0 {
		return [2]string{s, ""}
	}
	return [2]string{s[:idx], s[idx+len(sep):]}
}
