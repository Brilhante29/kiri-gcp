package kms

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "kms"

func init() { service.Register(New()) }

type keyRing struct {
	Name       string `json:"name"`
	CreateTime string `json:"createTime"`
}

type cryptoKey struct {
	Name           string `json:"name"`
	Purpose        string `json:"purpose"`
	CreateTime     string `json:"createTime"`
	PrimaryVersion string `json:"primary"`
}

type state struct {
	KeyRings   map[string]*keyRing   `json:"keyRings"`   // key: name
	CryptoKeys map[string]*cryptoKey `json:"cryptoKeys"` // key: name
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{
		st: state{
			KeyRings:   make(map[string]*keyRing),
			CryptoKeys: make(map[string]*cryptoKey),
		},
	}
	_ = storage.Load(serviceName, "state", &s.st)
	if s.st.KeyRings == nil {
		s.st.KeyRings = make(map[string]*keyRing)
	}
	if s.st.CryptoKeys == nil {
		s.st.CryptoKeys = make(map[string]*cryptoKey)
	}
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud KMS",
		Category:    "Security",
		Description: "Cryptographic key management and encryption service",
		State:       service.StateBehavioral,
		Fidelity:    service.FidelityA,
	}
}

func (s *Service) Close() error { return storage.Save(serviceName, "state", s.st) }

func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/keyRings", s.createKeyRing)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/keyRings", s.listKeyRings)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}", s.getKeyRing)

	r.Handle("POST", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys", s.createCryptoKey)
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys/{cryptoKey}/cryptoKeyVersions", s.createCryptoKeyVersion)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys", s.listCryptoKeys)
	r.Handle("GET", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys/{cryptoKey}", s.handleCryptoKeyOrAction)
	r.Handle("POST", "/v1/projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys/{cryptoKey}", s.handleCryptoKeyOrAction)
}

func (s *Service) createKeyRing(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	id := r.URL.Query().Get("keyRingId")
	if id == "" {
		id = r.URL.Query().Get("id")
	}
	if id == "" {
		var req struct {
			KeyRingID string `json:"keyRingId"`
		}
		_ = httpx.DecodeJSON(r, &req)
		id = req.KeyRingID
	}
	if id == "" {
		id = httpx.ID(8)
	}

	fullName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", project, location, id)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.KeyRings[fullName]; exists {
		httpx.AlreadyExists(w, fmt.Sprintf("KeyRing %s already exists", fullName))
		return
	}

	kr := &keyRing{
		Name:       fullName,
		CreateTime: httpx.Now(),
	}
	s.st.KeyRings[fullName] = kr
	httpx.WriteJSON(w, http.StatusOK, kr)
}

func (s *Service) listKeyRings(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	prefix := fmt.Sprintf("projects/%s/locations/%s/keyRings/", project, location)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*keyRing
	for name, kr := range s.st.KeyRings {
		if strings.HasPrefix(name, prefix) {
			result = append(result, kr)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"keyRings": result,
	})
}

func (s *Service) getKeyRing(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	keyRingName := r.PathValue("keyRing")
	fullName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", project, location, keyRingName)

	s.mu.RLock()
	kr, exists := s.st.KeyRings[fullName]
	s.mu.RUnlock()

	if !exists {
		httpx.NotFound(w, fmt.Sprintf("KeyRing %s not found", fullName))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, kr)
}

func (s *Service) createCryptoKey(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	krName := r.PathValue("keyRing")

	id := r.URL.Query().Get("cryptoKeyId")
	if id == "" {
		id = r.URL.Query().Get("id")
	}

	var req struct {
		Purpose string `json:"purpose"`
	}
	_ = httpx.DecodeJSON(r, &req)

	if id == "" {
		id = httpx.ID(8)
	}

	fullName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", project, location, krName, id)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.CryptoKeys[fullName]; exists {
		httpx.AlreadyExists(w, fmt.Sprintf("CryptoKey %s already exists", fullName))
		return
	}

	purpose := req.Purpose
	if purpose == "" {
		purpose = "ENCRYPT_DECRYPT"
	}

	ck := &cryptoKey{
		Name:           fullName,
		Purpose:        purpose,
		CreateTime:     httpx.Now(),
		PrimaryVersion: fullName + "/cryptoKeyVersions/1",
	}
	s.st.CryptoKeys[fullName] = ck
	httpx.WriteJSON(w, http.StatusOK, ck)
}

func (s *Service) listCryptoKeys(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	krName := r.PathValue("keyRing")
	prefix := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/", project, location, krName)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*cryptoKey
	for name, ck := range s.st.CryptoKeys {
		if strings.HasPrefix(name, prefix) {
			result = append(result, ck)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"cryptoKeys": result,
	})
}

func (s *Service) handleCryptoKeyOrAction(w http.ResponseWriter, r *http.Request) {
	rawKey := r.PathValue("cryptoKey")
	keyName, action := httpx.SplitVerb(rawKey)

	project := r.PathValue("project")
	location := r.PathValue("location")
	krName := r.PathValue("keyRing")
	fullName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", project, location, krName, keyName)

	switch action {
	case "encrypt":
		var req struct {
			Plaintext string `json:"plaintext"`
		}
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		ciphertext := base64.StdEncoding.EncodeToString([]byte("kms_enc:" + req.Plaintext))
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"name":       fullName,
			"ciphertext": ciphertext,
		})
	case "decrypt":
		var req struct {
			Ciphertext string `json:"ciphertext"`
		}
		if err := httpx.DecodeJSON(r, &req); err != nil {
			httpx.BadRequest(w, err.Error())
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(req.Ciphertext)
		plain := strings.TrimPrefix(string(raw), "kms_enc:")
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"plaintext": base64.StdEncoding.EncodeToString([]byte(plain)),
		})
	default:
		if r.Method == http.MethodGet {
			s.mu.RLock()
			ck, exists := s.st.CryptoKeys[fullName]
			s.mu.RUnlock()
			if !exists {
				httpx.NotFound(w, fmt.Sprintf("CryptoKey %s not found", fullName))
				return
			}
			httpx.WriteJSON(w, http.StatusOK, ck)
		} else {
			httpx.BadRequest(w, "invalid request")
		}
	}
}

func (s *Service) createCryptoKeyVersion(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	location := r.PathValue("location")
	krName := r.PathValue("keyRing")
	keyName := r.PathValue("cryptoKey")

	fullKey := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s", project, location, krName, keyName)
	versionName := fmt.Sprintf("%s/cryptoKeyVersions/1", fullKey)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":  versionName,
		"state": "ENABLED",
	})
}
