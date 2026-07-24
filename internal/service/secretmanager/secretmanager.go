// Package secretmanager emulates Google Secret Manager's REST API v1:
// secret CRUD, incrementing versions, and version access. The real API passes
// secretId as a query parameter on create and uses ":access"/":addVersion"
// custom methods, both of which are handled here.
package secretmanager

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "secretmanager"

func init() { service.Register(New()) }

type state struct {
	Secrets  map[string]map[string]any `json:"secrets"`
	Versions map[string]map[string]any `json:"versions"`
	// Counter tracks the next version number per secret name.
	Counter map[string]int `json:"counter"`
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{st: state{
		Secrets:  make(map[string]map[string]any),
		Versions: make(map[string]map[string]any),
		Counter:  make(map[string]int),
	}}
	_ = storage.Load(serviceName, "state", &s.st)

	if s.st.Secrets == nil {
		s.st.Secrets = make(map[string]map[string]any)
	}

	if s.st.Versions == nil {
		s.st.Versions = make(map[string]map[string]any)
	}

	if s.st.Counter == nil {
		s.st.Counter = make(map[string]int)
	}

	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) Meta() service.Meta {
	return service.Meta{Display: "Secret Manager", Category: "Security", Description: "Secret storage and versioning", State: service.StateBehavioral, Fidelity: service.FidelityA}
}

func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.st)
}

func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/projects/{project}/secrets", s.create)
	r.Handle("GET", "/v1/projects/{project}/secrets", s.list)
	r.Handle("GET", "/v1/projects/{project}/secrets/{secret}", s.get)
	r.Handle("DELETE", "/v1/projects/{project}/secrets/{secret}", s.delete)
	// {secret} also matches "name:addVersion" custom-method segments.
	r.Handle("POST", "/v1/projects/{project}/secrets/{secret}", s.secretAction)
	r.Handle("POST", "/v1/projects/{project}/secrets/{secret}/versions", s.addVersion)
	// {version} also matches "1:access" custom-method segments.
	r.Handle("GET", "/v1/projects/{project}/secrets/{secret}/versions/{version}", s.access)
}

func (s *Service) create(w http.ResponseWriter, r *http.Request) {
	// The real API passes secretId as a query parameter; accept a body field
	// as a lenient fallback.
	secID := r.URL.Query().Get("secretId")

	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req == nil {
		req = map[string]any{}
	}

	if secID == "" {
		if v, ok := req["secretId"].(string); ok {
			secID = v
		}
	}

	if secID == "" {
		httpx.BadRequest(w, "secretId query parameter is required")

		return
	}

	name := "projects/" + r.PathValue("project") + "/secrets/" + secID

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Secrets[name]; exists {
		httpx.AlreadyExists(w, "secret already exists: "+name)

		return
	}

	req["name"] = name
	req["createTime"] = httpx.Now()
	s.st.Secrets[name] = req

	httpx.WriteJSON(w, http.StatusOK, req)
}

func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	prefix := "projects/" + r.PathValue("project") + "/secrets/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	secrets := make([]any, 0)

	for name, sec := range s.st.Secrets {
		if strings.HasPrefix(name, prefix) {
			secrets = append(secrets, sec)
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"secrets": secrets})
}

func (s *Service) get(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/secrets/" + r.PathValue("secret")

	s.mu.RLock()
	sec, ok := s.st.Secrets[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "secret not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, sec)
}

func (s *Service) delete(w http.ResponseWriter, r *http.Request) {
	name := "projects/" + r.PathValue("project") + "/secrets/" + r.PathValue("secret")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Secrets[name]; !ok {
		httpx.NotFound(w, "secret not found: "+name)

		return
	}

	delete(s.st.Secrets, name)

	// Drop the secret's versions and counter as well.
	versionPrefix := name + "/versions/"
	for vname := range s.st.Versions {
		if strings.HasPrefix(vname, versionPrefix) {
			delete(s.st.Versions, vname)
		}
	}

	delete(s.st.Counter, name)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// secretAction handles POST custom methods on a secret, e.g. ":addVersion".
func (s *Service) secretAction(w http.ResponseWriter, r *http.Request) {
	id, verb := httpx.SplitVerb(r.PathValue("secret"))

	if verb != "addVersion" {
		httpx.NotFound(w, "unknown method: "+verb)

		return
	}

	s.storeVersion(w, r, id)
}

// addVersion handles the plain collection form POST .../secrets/{secret}/versions.
func (s *Service) addVersion(w http.ResponseWriter, r *http.Request) {
	s.storeVersion(w, r, r.PathValue("secret"))
}

func (s *Service) storeVersion(w http.ResponseWriter, r *http.Request, secretID string) {
	secretName := "projects/" + r.PathValue("project") + "/secrets/" + secretID

	var req map[string]any
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req == nil {
		req = map[string]any{}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.st.Secrets[secretName]; !ok {
		httpx.NotFound(w, "secret not found: "+secretName)

		return
	}

	s.st.Counter[secretName]++
	version := strconv.Itoa(s.st.Counter[secretName])
	name := secretName + "/versions/" + version

	req["name"] = name
	req["state"] = "ENABLED"
	req["createTime"] = httpx.Now()
	s.st.Versions[name] = req

	httpx.WriteJSON(w, http.StatusOK, req)
}

func (s *Service) access(w http.ResponseWriter, r *http.Request) {
	ver, verb := httpx.SplitVerb(r.PathValue("version"))
	name := "projects/" + r.PathValue("project") + "/secrets/" + r.PathValue("secret") + "/versions/" + resolveVersion(ver)

	s.mu.RLock()
	defer s.mu.RUnlock()

	// "latest" resolves to the highest version number for the secret.
	if ver == "latest" {
		secretName := "projects/" + r.PathValue("project") + "/secrets/" + r.PathValue("secret")
		name = secretName + "/versions/" + strconv.Itoa(s.st.Counter[secretName])
	}

	val, ok := s.st.Versions[name]
	if !ok {
		httpx.NotFound(w, "version not found: "+name)

		return
	}

	if verb == "access" {
		// AccessSecretVersion returns the name plus the stored payload.
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"name":    name,
			"payload": val["payload"],
		})

		return
	}

	httpx.WriteJSON(w, http.StatusOK, val)
}

func resolveVersion(v string) string { return v }
