// Package visionai emulates Vision AI (vision.googleapis.com/v1): image
// annotation requests, returning deterministic stub labels/text per feature
// type requested (real detection is out of scope for an emulator).
package visionai

import (
	"net/http"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "visionai"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	service.Register(svc)
}

type state struct {
	AnnotateCount int `json:"annotateCount"`
}

// Service implements the Vision AI emulation.
type Service struct {
	mu sync.Mutex
	st state
}

// New creates an empty Vision AI service.
func New() *Service { return &Service{} }

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Vision AI",
		Category:    "Analytics & ML",
		Description: "Image annotation requests (labels, text, faces)",
		Fidelity:    service.FidelityB,
		State:       service.StateBehavioral,
	}
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return storage.Save(serviceName, "state", s.st)
}

// RegisterRoutes registers the Vision AI REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/images:annotate", s.annotate)
}

func (s *Service) annotate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Requests []struct {
			Features []struct {
				Type string `json:"type"`
			} `json:"features"`
		} `json:"requests"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()
	s.st.AnnotateCount += len(body.Requests)
	s.mu.Unlock()

	responses := make([]map[string]any, 0, len(body.Requests))

	for _, req := range body.Requests {
		resp := map[string]any{}

		for _, f := range req.Features {
			switch f.Type {
			case "LABEL_DETECTION":
				resp["labelAnnotations"] = []map[string]any{
					{"description": "object", "score": 0.9},
				}
			case "TEXT_DETECTION":
				resp["textAnnotations"] = []map[string]any{
					{"description": "sample text", "locale": "en"},
				}
			case "FACE_DETECTION":
				resp["faceAnnotations"] = []map[string]any{}
			default:
				resp["labelAnnotations"] = []map[string]any{}
			}
		}

		responses = append(responses, resp)
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"responses": responses})
}
