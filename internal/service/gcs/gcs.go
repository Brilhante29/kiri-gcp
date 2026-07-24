// Package gcs emulates Google Cloud Storage: the JSON API (storage/v1) for
// bucket/object management, plus the XML-API-style root-level download path
// ("GET /{bucket}/{object}") that the official Go client's object Reader
// actually issues by default — verified against the real
// cloud.google.com/go/storage SDK, not assumed from the JSON API docs alone.
package gcs

import (
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "gcs"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	svc.ensureMaps()
	service.Register(svc)
}

// PublishFunc delivers a GCS bucket-notification event to Pub/Sub. Wired by
// the server to the real Pub/Sub service.
var PublishFunc func(topicPath, data string, attrs map[string]string) []string

// Bucket is a stored GCS bucket.
type Bucket struct {
	Name string `json:"name"`
}

// Object is a stored GCS object with its bytes.
type Object struct {
	Name        string `json:"name"`
	ContentType string `json:"contentType,omitempty"`
	Data        []byte `json:"data"`
}

type state struct {
	Buckets map[string]*Bucket            `json:"buckets"`
	Objects map[string]map[string]*Object `json:"objects"` // bucket -> object name -> object
}

// Service implements the Cloud Storage emulation.
type Service struct {
	mu sync.RWMutex
	st state
}

// New creates an empty Cloud Storage service.
func New() *Service {
	return &Service{st: state{Buckets: map[string]*Bucket{}, Objects: map[string]map[string]*Object{}}}
}

func (s *Service) ensureMaps() {
	if s.st.Buckets == nil {
		s.st.Buckets = map[string]*Bucket{}
	}

	if s.st.Objects == nil {
		s.st.Objects = map[string]map[string]*Object{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Storage",
		Category:    "Storage",
		Description: "Object storage: JSON API + root-level media downloads (real SDK Reader path)",
		Fidelity:    service.FidelityA,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the GCS JSON API routes plus the root-level
// media-download route the real Reader uses.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/storage/v1/b", s.createBucket)
	r.Handle("GET", "/storage/v1/b", s.listBuckets)
	r.Handle("GET", "/storage/v1/b/{bucket}", s.getBucket)
	r.Handle("DELETE", "/storage/v1/b/{bucket}", s.deleteBucket)

	r.Handle("POST", "/upload/storage/v1/b/{bucket}/o", s.uploadObj)
	r.Handle("GET", "/storage/v1/b/{bucket}/o", s.listObjs)
	r.Handle("GET", "/storage/v1/b/{bucket}/o/{obj...}", s.getObj)
	r.Handle("DELETE", "/storage/v1/b/{bucket}/o/{obj...}", s.deleteObj)

	// Root-level media download: what the real storage Go client's
	// object.NewReader actually requests by default, distinct from the JSON
	// API's "?alt=media" convention above. Both are kept working.
	r.Handle("GET", "/{bucket}/{obj...}", s.getObjectMedia)
}

func (s *Service) createBucket(w http.ResponseWriter, r *http.Request) {
	var b Bucket
	if err := httpx.DecodeJSON(r, &b); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if b.Name == "" {
		httpx.BadRequest(w, "name is required")

		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Buckets[b.Name]; exists {
		httpx.AlreadyExists(w, "bucket already exists: "+b.Name)

		return
	}

	s.st.Buckets[b.Name] = &b
	s.st.Objects[b.Name] = map[string]*Object{}

	httpx.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) listBuckets(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.st.Buckets))
	for n := range s.st.Buckets {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]*Bucket, 0, len(names))
	for _, n := range names {
		items = append(items, s.st.Buckets[n])
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getBucket(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")

	s.mu.RLock()
	b, ok := s.st.Buckets[bucket]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "bucket not found: "+bucket)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) deleteBucket(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Buckets[bucket]; !ok {
		httpx.NotFound(w, "bucket not found: "+bucket)

		return
	}

	delete(s.st.Buckets, bucket)
	delete(s.st.Objects, bucket)

	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) uploadObj(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")

	s.mu.RLock()
	_, bucketExists := s.st.Buckets[bucket]
	s.mu.RUnlock()

	if !bucketExists {
		httpx.NotFound(w, "bucket not found: "+bucket)

		return
	}

	var (
		name        string
		contentType string
		data        []byte
	)

	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "multipart/") {
		n, c, body, err := parseMultipartUpload(r, ct)
		if err != nil {
			httpx.BadRequest(w, "parse multipart upload: "+err.Error())

			return
		}

		name, contentType, data = n, c, body
	} else {
		name = r.URL.Query().Get("name")
		contentType = ct

		body, err := io.ReadAll(r.Body)
		if err != nil {
			httpx.BadRequest(w, "read body: "+err.Error())

			return
		}

		data = body
	}

	if name == "" {
		httpx.BadRequest(w, "object name is required (?name= for media uploads, metadata.name for multipart)")

		return
	}

	obj := &Object{Name: name, ContentType: contentType, Data: data}

	s.mu.Lock()
	if s.st.Objects[bucket] == nil {
		s.st.Objects[bucket] = map[string]*Object{}
	}

	s.st.Objects[bucket][name] = obj
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, objectResource(bucket, obj))
}

// parseMultipartUpload parses a GCS multipart upload: the first part is a
// JSON metadata object (must contain "name"), the second is the object bytes.
func parseMultipartUpload(r *http.Request, contentType string) (name, ct string, data []byte, err error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", "", nil, err
	}

	mr := multipart.NewReader(r.Body, params["boundary"])

	metaPart, err := mr.NextPart()
	if err != nil {
		return "", "", nil, err
	}

	var meta struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
	}

	if err := httpx.DecodeJSON(&http.Request{Body: io.NopCloser(metaPart)}, &meta); err != nil {
		return "", "", nil, err
	}

	dataPart, err := mr.NextPart()
	if err != nil {
		return "", "", nil, err
	}

	body, err := io.ReadAll(dataPart)
	if err != nil {
		return "", "", nil, err
	}

	ct = meta.ContentType
	if ct == "" {
		ct = dataPart.Header.Get("Content-Type")
	}

	return meta.Name, ct, body, nil
}

func (s *Service) listObjs(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	prefix := r.URL.Query().Get("prefix")

	s.mu.RLock()
	defer s.mu.RUnlock()

	objs, ok := s.st.Objects[bucket]
	if !ok {
		httpx.NotFound(w, "bucket not found: "+bucket)

		return
	}

	names := make([]string, 0, len(objs))
	for n := range objs {
		if prefix == "" || strings.HasPrefix(n, prefix) {
			names = append(names, n)
		}
	}

	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, objectResource(bucket, objs[n]))
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Service) getObj(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	name := r.PathValue("obj")

	s.mu.RLock()
	obj, ok := s.lookup(bucket, name)
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "object not found: "+name)

		return
	}

	if r.URL.Query().Get("alt") == "media" {
		s.writeMedia(w, obj)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, objectResource(bucket, obj))
}

// getObjectMedia serves "GET /{bucket}/{object}" — the root-level path the
// real storage client's Reader issues by default (not the JSON API's
// "?alt=media" convention, which getObj above also supports).
func (s *Service) getObjectMedia(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	name := r.PathValue("obj")

	s.mu.RLock()
	obj, ok := s.lookup(bucket, name)
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "object not found: "+bucket+"/"+name)

		return
	}

	s.writeMedia(w, obj)
}

func (s *Service) writeMedia(w http.ResponseWriter, obj *Object) {
	ct := obj.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}

	w.Header().Set("Content-Type", ct)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(obj.Data)
}

func (s *Service) lookup(bucket, name string) (*Object, bool) {
	objs, ok := s.st.Objects[bucket]
	if !ok {
		return nil, false
	}

	obj, ok := objs[name]

	return obj, ok
}

func (s *Service) deleteObj(w http.ResponseWriter, r *http.Request) {
	bucket := r.PathValue("bucket")
	name := r.PathValue("obj")

	s.mu.Lock()
	defer s.mu.Unlock()

	objs, ok := s.st.Objects[bucket]
	if !ok {
		httpx.NotFound(w, "bucket not found: "+bucket)

		return
	}

	if _, ok := objs[name]; !ok {
		httpx.NotFound(w, "object not found: "+name)

		return
	}

	delete(objs, name)

	w.WriteHeader(http.StatusNoContent)
}

// objectResource renders the JSON API object resource. "size" (and every
// other int64 field in the real API, e.g. generation) is wire-encoded as a
// decimal STRING, not a JSON number — the Go client's raw response struct
// uses a `,string` struct tag for these and fails to unmarshal a bare
// number. Confirmed against the real SDK, not assumed from docs.
func objectResource(bucket string, o *Object) map[string]any {
	return map[string]any{
		"kind":        "storage#object",
		"bucket":      bucket,
		"name":        o.Name,
		"contentType": o.ContentType,
		"size":        strconv.Itoa(len(o.Data)),
	}
}
