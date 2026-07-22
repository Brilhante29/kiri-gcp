// Package firestoregrpc implements the gRPC Firestore service
// (google.firestore.v1.Firestore) for the kiri emulator. It wraps the
// REST Firestore service logic so clients using FIRESTORE_EMULATOR_HOST
// get drop-in compatibility.
package firestoregrpc

import (
	"context"
	"sync"

	"github.com/kiri-dev/kiri/internal/protow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Backend abstracts the Firestore storage layer.
type Backend interface {
	GetDocument(name string) (doc *protow.DocumentMsg, ok bool)
	ListDocuments(parent string) []*protow.DocumentMsg
	CreateDocument(parent, docID string) *protow.DocumentMsg
	DeleteDocument(name string) bool
}

type grpcService struct {
	backend Backend
}

type firestoreServer interface{}

// RegisterWith registers the Firestore gRPC service.
func RegisterWith(b Backend) func(*grpc.Server) {
	return func(s *grpc.Server) {
		svc := &grpcService{backend: b}
		s.RegisterService(&firestoreServiceDesc, svc)
	}
}

// MergeBackend adapts the Firestore REST service state for gRPC.
type MergeBackend struct {
	mu        sync.RWMutex
	Documents map[string]*protow.DocumentMsg
}

func NewMergeBackend() *MergeBackend {
	return &MergeBackend{Documents: map[string]*protow.DocumentMsg{}}
}

func (b *MergeBackend) GetDocument(name string) (*protow.DocumentMsg, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	doc, ok := b.Documents[name]
	return doc, ok
}

func (b *MergeBackend) ListDocuments(parent string) []*protow.DocumentMsg {
	b.mu.RLock()
	defer b.mu.RUnlock()
	prefix := parent
	if prefix == "" || prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}
	var out []*protow.DocumentMsg
	for name, doc := range b.Documents {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			out = append(out, doc)
		}
	}
	return out
}

func (b *MergeBackend) CreateDocument(parent, docID string) *protow.DocumentMsg {
	b.mu.Lock()
	defer b.mu.Unlock()
	name := parent + "/" + docID
	doc := &protow.DocumentMsg{
		Name:       name,
		CreateTime: protow.Now(),
		UpdateTime: protow.Now(),
	}
	b.Documents[name] = doc
	return doc
}

func (b *MergeBackend) DeleteDocument(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.Documents[name]; !ok {
		return false
	}
	delete(b.Documents, name)
	return true
}

// Now returns RFC3339 timestamp. Duplicated from httpx to avoid import cycle.
func now() string { return protow.Now() }

// runUnary invokes the server-wide unary interceptor (if any) around fn,
// matching the dispatch pattern protoc-gen-go-grpc generates. Mirrors the
// pubsubgrpc package's helper of the same name — the same interceptor-bypass
// bug (handlers discarding the interceptor parameter, silently disabling
// request-logging) applies here and needed the same fix.
func runUnary(ctx context.Context, srv any, fullMethod string, interceptor grpc.UnaryServerInterceptor, fn func() (any, error)) (any, error) {
	if interceptor == nil {
		return fn()
	}

	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: fullMethod}
	handler := func(ctx context.Context, _ any) (any, error) { return fn() }

	return interceptor(ctx, struct{}{}, info, handler)
}

// --- gRPC Firestore service ---

var firestoreServiceDesc = grpc.ServiceDesc{
	ServiceName: "google.firestore.v1.Firestore",
	HandlerType: (*firestoreServer)(nil),
	Methods: []grpc.MethodDesc{
		{MethodName: "GetDocument", Handler: _Firestore_GetDocument_Handler},
		{MethodName: "ListDocuments", Handler: _Firestore_ListDocuments_Handler},
		{MethodName: "CreateDocument", Handler: _Firestore_CreateDocument_Handler},
		{MethodName: "DeleteDocument", Handler: _Firestore_DeleteDocument_Handler},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "google/firestore/v1/firestore.proto",
}

func _Firestore_GetDocument_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeGetDocumentRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.firestore.v1.Firestore/GetDocument", interceptor, func() (any, error) {
		doc, ok := svc.backend.GetDocument(req.Name)
		if !ok {
			return nil, status.Errorf(codes.NotFound, "document not found: %s", req.Name)
		}

		return doc, nil
	})
}

func _Firestore_ListDocuments_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeListDocumentsRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.firestore.v1.Firestore/ListDocuments", interceptor, func() (any, error) {
		docs := svc.backend.ListDocuments(req.Parent)

		return &protow.ListDocumentsResponse{Documents: docs}, nil
	})
}

func _Firestore_CreateDocument_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeCreateDocumentRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.firestore.v1.Firestore/CreateDocument", interceptor, func() (any, error) {
		return svc.backend.CreateDocument(req.Parent, req.DocumentId), nil
	})
}

func _Firestore_DeleteDocument_Handler(srv any, ctx context.Context, dec func(any) error, interceptor grpc.UnaryServerInterceptor) (any, error) {
	var raw []byte
	if err := dec(&raw); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "decode request: %v", err)
	}

	req := protow.DecodeGetDocumentRequest(raw)
	svc := srv.(*grpcService)

	return runUnary(ctx, srv, "/google.firestore.v1.Firestore/DeleteDocument", interceptor, func() (any, error) {
		if !svc.backend.DeleteDocument(req.Name) {
			return nil, status.Errorf(codes.NotFound, "document not found: %s", req.Name)
		}

		return &protow.Empty{}, nil
	})
}
