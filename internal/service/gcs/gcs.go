// Package gcs emulates Google Cloud Storage: the JSON API (storage/v1) for
// bucket/object management, plus the XML-API-style root-level download path
// ("GET /{bucket}/{object}") that the official Go client's object Reader
// actually issues by default — verified against the real
// cloud.google.com/go/storage SDK, not assumed from the JSON API docs alone.
package gcs

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
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
	mu       sync.RWMutex
	st       state
	sessions map[string]*uploadSession // resumable uploads by upload_id
}

// uploadSession is an in-progress resumable upload. Sessions live in memory
// only; a restart abandons them, as an expired session would.
type uploadSession struct {
	bucket      string
	name        string
	contentType string
	data        []byte
}

// New creates an empty Cloud Storage service.
func New() *Service {
	return &Service{
		st:       state{Buckets: map[string]*Bucket{}, Objects: map[string]map[string]*Object{}},
		sessions: map[string]*uploadSession{},
	}
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

// RegisterRoutes registers the GCS JSON API routes plus the media-download
// routes the official clients use.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/storage/v1/b", s.createBucket)
	r.Handle("GET", "/storage/v1/b", s.listBuckets)
	r.Handle("GET", "/storage/v1/b/{bucket}", s.getBucket)
	r.Handle("DELETE", "/storage/v1/b/{bucket}", s.deleteBucket)

	r.Handle("POST", "/upload/storage/v1/b/{bucket}/o", s.uploadObj)
	r.Handle("PUT", "/upload/storage/v1/b/{bucket}/o", s.resumeUpload)
	r.Handle("GET", "/storage/v1/b/{bucket}/o", s.listObjs)
	r.Handle("GET", "/storage/v1/b/{bucket}/o/{obj...}", s.getObj)
	r.Handle("DELETE", "/storage/v1/b/{bucket}/o/{obj...}", s.deleteObj)

	// JSON API media-download path: what the Python client requests for
	// blob downloads ("/download/storage/v1/b/{bucket}/o/{object}?alt=media").
	// Without it the request falls through to the root-level route below and
	// is read as bucket "download".
	r.Handle("GET", "/download/storage/v1/b/{bucket}/o/{obj...}", s.getObjectMedia)

	// Root-level media download: what the real storage Go client's
	// object.NewReader actually requests by default, distinct from the JSON
	// API's "?alt=media" convention above. All three are kept working.
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

	httpx.WriteJSON(w, http.StatusOK, bucketResource(&b))
}

func (s *Service) listBuckets(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	names := make([]string, 0, len(s.st.Buckets))
	for n := range s.st.Buckets {
		names = append(names, n)
	}

	sort.Strings(names)

	items := make([]map[string]any, 0, len(names))
	for _, n := range names {
		items = append(items, bucketResource(s.st.Buckets[n]))
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

	httpx.WriteJSON(w, http.StatusOK, bucketResource(b))
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

	// The Go client sends resumable chunks with POST to the session URI.
	if r.URL.Query().Get("upload_id") != "" {
		s.resumeUpload(w, r)

		return
	}

	if r.URL.Query().Get("uploadType") == "resumable" {
		s.startResumable(w, r, bucket)

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

		// As in the real API, ?name= overrides the metadata name; the Node.js
		// client sends simple uploads with the name only in the query string.
		if q := r.URL.Query().Get("name"); q != "" {
			name = q
		}
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

// startResumable opens a resumable upload session and returns its URI in the
// Location header, as the real API does. The Node.js and Java clients upload
// this way by default, and the Python and Go clients do for large payloads.
func (s *Service) startResumable(w http.ResponseWriter, r *http.Request, bucket string) {
	var meta struct {
		Name        string `json:"name"`
		ContentType string `json:"contentType"`
	}

	if err := httpx.DecodeJSON(r, &meta); err != nil {
		httpx.BadRequest(w, "parse resumable metadata: "+err.Error())

		return
	}

	if q := r.URL.Query().Get("name"); q != "" {
		meta.Name = q
	}

	if meta.Name == "" {
		httpx.BadRequest(w, "object name is required (?name= or metadata.name)")

		return
	}

	if meta.ContentType == "" {
		meta.ContentType = r.Header.Get("X-Upload-Content-Type")
	}

	id := httpx.ID(16)

	s.mu.Lock()
	s.sessions[id] = &uploadSession{bucket: bucket, name: meta.Name, contentType: meta.ContentType}
	s.mu.Unlock()

	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	w.Header().Set("Location", scheme+"://"+r.Host+"/upload/storage/v1/b/"+url.PathEscape(bucket)+"/o?uploadType=resumable&upload_id="+id)
	w.Header().Set("X-GUploader-UploadID", id)
	w.WriteHeader(http.StatusOK)
}

// resumeUpload receives data for a resumable session, by PUT or POST to the
// session URI. A request carries the whole object (no Content-Range), one
// chunk ("bytes 0-262143/*"), the rest of the stream ("bytes 0-*/*"), or no
// data at all for a status check ("bytes */*"). An unfinished upload answers
// 308 with the persisted range.
func (s *Service) resumeUpload(w http.ResponseWriter, r *http.Request) {
	cr, err := parseContentRange(r.Header.Get("Content-Range"))
	if err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		httpx.BadRequest(w, "read body: "+err.Error())

		return
	}

	id := r.URL.Query().Get("upload_id")

	s.mu.Lock()

	sess, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		httpx.NotFound(w, "upload session not found: "+id)

		return
	}

	// A chunk that does not continue the persisted bytes is not applied; the
	// 308 below tells the client where to resume.
	applied := !cr.query && cr.start == int64(len(sess.data))
	if applied {
		sess.data = append(sess.data, body...)
	}

	persisted := len(sess.data)
	complete := (applied && cr.last) || (cr.total >= 0 && int64(persisted) == cr.total)

	if !complete {
		s.mu.Unlock()
		writeResumeIncomplete(w, r, persisted)

		return
	}

	delete(s.sessions, id)

	if _, exists := s.st.Buckets[sess.bucket]; !exists {
		s.mu.Unlock()
		httpx.NotFound(w, "bucket not found: "+sess.bucket)

		return
	}

	obj := &Object{Name: sess.name, ContentType: sess.contentType, Data: sess.data}
	if s.st.Objects[sess.bucket] == nil {
		s.st.Objects[sess.bucket] = map[string]*Object{}
	}

	s.st.Objects[sess.bucket][sess.name] = obj
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, objectResource(sess.bucket, obj))
}

// contentRange is a parsed resumable-upload Content-Range header.
type contentRange struct {
	start int64 // offset of the request body within the object
	total int64 // object size, or -1 while the client does not know it
	query bool  // "bytes */total": a status check that carries no data
	last  bool  // no header, or "bytes start-*/total": the body ends the object
}

func parseContentRange(header string) (contentRange, error) {
	if header == "" {
		return contentRange{total: -1, last: true}, nil
	}

	spec, ok := strings.CutPrefix(header, "bytes ")
	span, size, hasSize := strings.Cut(spec, "/")

	if !ok || !hasSize {
		return contentRange{}, fmt.Errorf("unsupported Content-Range %q", header)
	}

	cr := contentRange{total: -1}

	if size != "*" {
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil || n < 0 {
			return contentRange{}, fmt.Errorf("invalid Content-Range size in %q", header)
		}

		cr.total = n
	}

	if span == "*" {
		cr.query = true

		return cr, nil
	}

	first, end, ok := strings.Cut(span, "-")
	if !ok {
		return contentRange{}, fmt.Errorf("unsupported Content-Range %q", header)
	}

	start, err := strconv.ParseInt(first, 10, 64)
	if err != nil || start < 0 {
		return contentRange{}, fmt.Errorf("invalid Content-Range start in %q", header)
	}

	cr.start = start
	cr.last = end == "*"

	return cr, nil
}

// writeResumeIncomplete answers 308 with the persisted byte range. The Range
// header is omitted while nothing is stored, as in the real API. Clients that
// send "X-GUploader-No-308: yes" (the Go client) get 200 with the status in
// X-HTTP-Status-Code-Override instead, because 308 also means a redirect.
func writeResumeIncomplete(w http.ResponseWriter, r *http.Request, persisted int) {
	if persisted > 0 {
		w.Header().Set("Range", "bytes=0-"+strconv.Itoa(persisted-1))
	}

	if r.Header.Get("X-GUploader-No-308") == "yes" {
		w.Header().Set("X-HTTP-Status-Code-Override", "308")
		w.WriteHeader(http.StatusOK)

		return
	}

	w.WriteHeader(http.StatusPermanentRedirect)
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

	md5Hash, crc32c := checksums(obj.Data)

	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Goog-Hash", "crc32c="+crc32c+",md5="+md5Hash)
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

// bucketResource renders the JSON API bucket resource. Clients address buckets
// by "id" as well as "name"; the Node.js client builds each listed bucket
// from its id.
func bucketResource(b *Bucket) map[string]any {
	return map[string]any{
		"kind": "storage#bucket",
		"id":   b.Name,
		"name": b.Name,
	}
}

// objectResource renders the JSON API object resource. "size" (and every
// other int64 field in the real API, e.g. generation) is wire-encoded as a
// decimal STRING, not a JSON number — the Go client's raw response struct
// uses a `,string` struct tag for these and fails to unmarshal a bare
// number. Confirmed against the real SDK, not assumed from docs.
func objectResource(bucket string, o *Object) map[string]any {
	md5Hash, crc32c := checksums(o.Data)

	return map[string]any{
		"kind":        "storage#object",
		"bucket":      bucket,
		"name":        o.Name,
		"contentType": o.ContentType,
		"size":        strconv.Itoa(len(o.Data)),
		"md5Hash":     md5Hash,
		"crc32c":      crc32c,
	}
}

// castagnoli is the CRC32C table GCS uses for object checksums.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// checksums returns the base64 MD5 and big-endian CRC32C of data, the two
// object checksums the API reports. Clients verify uploads against them; the
// Node.js client deletes an upload whose checksums are missing or wrong.
func checksums(data []byte) (md5Hash, crc32c string) {
	sum := md5.Sum(data)

	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], crc32.Checksum(data, castagnoli))

	return base64.StdEncoding.EncodeToString(sum[:]), base64.StdEncoding.EncodeToString(crc[:])
}
