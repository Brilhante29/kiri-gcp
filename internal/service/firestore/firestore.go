package firestore

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "firestore"

func init() { service.Register(New()) }

type Document struct {
	Name   string         `json:"name"`
	Fields map[string]any `json:"fields"`
}

type state struct {
	Documents map[string]*Document `json:"documents"`
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{
		st: state{Documents: make(map[string]*Document)},
	}
	_ = storage.Load(serviceName, "state", &s.st)
	if s.st.Documents == nil {
		s.st.Documents = make(map[string]*Document)
	}
	return s
}

func (s *Service) Name() string { return serviceName }
func (s *Service) Meta() service.Meta {
	return service.Meta{Display: "firestore", Category: "Databases", Description: "Cloud Firestore", State: service.StateBehavioral, Fidelity: service.FidelityB}
}
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return storage.Save(serviceName, "state", s.st)
}

func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("GET", "/v1/projects/{project}/databases/{db}/documents/{collection}/{doc}", s.getDoc)
	r.Handle("POST", "/v1/projects/{project}/databases/{db}/documents/{collection}", s.createDoc)
	r.Handle("GET", "/v1/projects/{project}/databases/{db}/documents/{collection}", s.listDocs)
	r.Handle("PATCH", "/v1/projects/{project}/databases/{db}/documents/{collection}/{doc}", s.patchDoc)
	r.Handle("DELETE", "/v1/projects/{project}/databases/{db}/documents/{collection}/{doc}", s.deleteDoc)
	r.Handle("POST", "/v1/projects/{project}/databases/{db}/documents:commit", s.commit)
	r.Handle("POST", "/v1/projects/{project}/databases/{db}/documents:beginTransaction", s.beginTx)
}

func (s *Service) getDoc(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project := r.PathValue("project")
	db := r.PathValue("db")
	coll := r.PathValue("collection")
	docID := r.PathValue("doc")

	name := "projects/" + project + "/databases/" + db + "/documents/" + coll + "/" + docID

	if doc, ok := s.st.Documents[name]; ok {
		_ = json.NewEncoder(w).Encode(doc)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Service) createDoc(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project := r.PathValue("project")
	db := r.PathValue("db")
	coll := r.PathValue("collection")
	docID := r.URL.Query().Get("documentId")
	if docID == "" {
		docID = "auto-id"
	}

	name := "projects/" + project + "/databases/" + db + "/documents/" + coll + "/" + docID

	var req struct {
		Document Document `json:"document"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	req.Document.Name = name
	if req.Document.Fields == nil {
		req.Document.Fields = make(map[string]any)
	}

	s.st.Documents[name] = &req.Document
	_ = json.NewEncoder(w).Encode(req.Document)
}

func (s *Service) listDocs(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	project := r.PathValue("project")
	db := r.PathValue("db")
	coll := r.PathValue("collection")
	prefix := "projects/" + project + "/databases/" + db + "/documents/" + coll + "/"

	var docs []Document
	for k, v := range s.st.Documents {
		if strings.HasPrefix(k, prefix) && !strings.Contains(k[len(prefix):], "/") {
			docs = append(docs, *v)
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"documents": docs})
}

func (s *Service) patchDoc(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project := r.PathValue("project")
	db := r.PathValue("db")
	coll := r.PathValue("collection")
	docID := r.PathValue("doc")
	name := "projects/" + project + "/databases/" + db + "/documents/" + coll + "/" + docID

	var req struct {
		Document Document `json:"document"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	doc, ok := s.st.Documents[name]
	if !ok {
		doc = &Document{Name: name, Fields: make(map[string]any)}
		s.st.Documents[name] = doc
	}
	if req.Document.Fields != nil {
		for k, v := range req.Document.Fields {
			doc.Fields[k] = v
		}
	}
	_ = json.NewEncoder(w).Encode(doc)
}

func (s *Service) deleteDoc(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	project := r.PathValue("project")
	db := r.PathValue("db")
	coll := r.PathValue("collection")
	docID := r.PathValue("doc")
	name := "projects/" + project + "/databases/" + db + "/documents/" + coll + "/" + docID

	delete(s.st.Documents, name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) beginTx(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{"transaction": "tx-123"})
}

func (s *Service) commit(w http.ResponseWriter, r *http.Request) {
	_ = json.NewEncoder(w).Encode(map[string]any{})
}
