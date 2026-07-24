// Package naturallanguage emulates Natural Language AI
// (language.googleapis.com/v1): sentiment and entity analysis over
// caller-supplied text, using simple heuristics (not real NLP) — enough for
// integration tests that assert on response shape and basic polarity.
package naturallanguage

import (
	"net/http"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri/internal/httpx"
	"github.com/Brilhante29/kiri/internal/service"
	"github.com/Brilhante29/kiri/internal/storage"
)

const serviceName = "naturallanguage"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.st)
	service.Register(svc)
}

type state struct {
	AnalysisCount int `json:"analysisCount"`
}

// Service implements the Natural Language AI emulation.
type Service struct {
	mu sync.Mutex
	st state
}

// New creates an empty Natural Language service.
func New() *Service { return &Service{} }

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Natural Language AI",
		Category:    "Analytics & ML",
		Description: "Sentiment and entity analysis over text",
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

// RegisterRoutes registers the Natural Language REST routes.
func (s *Service) RegisterRoutes(r service.Router) {
	r.Handle("POST", "/v1/documents:analyzeSentiment", s.analyzeSentiment)
	r.Handle("POST", "/v1/documents:analyzeEntities", s.analyzeEntities)
}

type document struct {
	Content string `json:"content"`
}

func (s *Service) decodeDocument(r *http.Request) (string, error) {
	var body struct {
		Document document `json:"document"`
	}
	if err := httpx.DecodeJSON(r, &body); err != nil {
		return "", err
	}

	return body.Document.Content, nil
}

// positiveWords/negativeWords drive a coarse lexicon-based sentiment score —
// real Natural Language AI uses a trained model; this is a stand-in with a
// deterministic, explainable rule instead.
var (
	positiveWords = []string{"good", "great", "excellent", "love", "happy", "amazing"}
	negativeWords = []string{"bad", "terrible", "hate", "awful", "sad", "worst"}
)

func (s *Service) analyzeSentiment(w http.ResponseWriter, r *http.Request) {
	content, err := s.decodeDocument(r)
	if err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	lower := strings.ToLower(content)

	score := 0.0
	for _, word := range positiveWords {
		if strings.Contains(lower, word) {
			score += 0.5
		}
	}

	for _, word := range negativeWords {
		if strings.Contains(lower, word) {
			score -= 0.5
		}
	}

	if score > 1 {
		score = 1
	}

	if score < -1 {
		score = -1
	}

	s.mu.Lock()
	s.st.AnalysisCount++
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"documentSentiment": map[string]any{"score": score, "magnitude": abs(score)},
		"language":          "en",
	})
}

func (s *Service) analyzeEntities(w http.ResponseWriter, r *http.Request) {
	content, err := s.decodeDocument(r)
	if err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	entities := make([]map[string]any, 0)

	for _, word := range strings.Fields(content) {
		trimmed := strings.Trim(word, ".,!?;:")
		if trimmed != "" && trimmed[0] >= 'A' && trimmed[0] <= 'Z' {
			entities = append(entities, map[string]any{"name": trimmed, "type": "OTHER", "salience": 0.5})
		}
	}

	s.mu.Lock()
	s.st.AnalysisCount++
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"entities": entities, "language": "en"})
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}

	return f
}
