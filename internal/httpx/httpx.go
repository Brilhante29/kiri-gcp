// Package httpx provides small REST/JSON helpers shared by every GCP service:
// canonical Google-style JSON responses and errors, request decoding, and ID
// generation. Keeping these in one place makes each service package terse and
// keeps the emulator's wire format consistent.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WriteJSON marshals v and writes it with the given HTTP status code.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)

	if v == nil {
		return
	}

	_ = json.NewEncoder(w).Encode(v)
}

// googleError is the standard Google Cloud JSON error envelope.
type googleError struct {
	Error googleErrorBody `json:"error"`
}

type googleErrorBody struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Status  string            `json:"status"`
	Errors  []googleErrorItem `json:"errors,omitempty"`
}

type googleErrorItem struct {
	Message string `json:"message"`
	Domain  string `json:"domain"`
	Reason  string `json:"reason"`
}

// Error writes a Google-style error response. status is the HTTP status;
// reason is the canonical status string, e.g. "NOT_FOUND", "ALREADY_EXISTS".
func Error(w http.ResponseWriter, status int, reason, message string) {
	WriteJSON(w, status, googleError{Error: googleErrorBody{
		Code:    status,
		Message: message,
		Status:  reason,
		Errors: []googleErrorItem{{
			Message: message,
			Domain:  "global",
			Reason:  reasonToCamel(reason),
		}},
	}})
}

// NotFound writes a canonical 404.
func NotFound(w http.ResponseWriter, message string) {
	Error(w, http.StatusNotFound, "NOT_FOUND", message)
}

// AlreadyExists writes a canonical 409.
func AlreadyExists(w http.ResponseWriter, message string) {
	Error(w, http.StatusConflict, "ALREADY_EXISTS", message)
}

// BadRequest writes a canonical 400.
func BadRequest(w http.ResponseWriter, message string) {
	Error(w, http.StatusBadRequest, "INVALID_ARGUMENT", message)
}

// DecodeJSON reads and unmarshals a JSON request body into v. An empty body is
// treated as an empty object (not an error), matching lenient GCP behavior.
func DecodeJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}

	return nil
}

// ID returns a random lowercase-hex identifier of n bytes (2n hex chars).
func ID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is fatal-adjacent; fall back to time-based bytes.
		for i := range b {
			b[i] = byte(time.Now().UnixNano() >> (i % 8))
		}
	}

	return hex.EncodeToString(b)
}

// NumericID returns a random positive int64 rendered as a decimal string, the
// shape GCP uses for project numbers, topic/subscription internal ids, etc.
func NumericID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}

	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}

	// Keep it positive and in a realistic 12-15 digit range.
	n = n%900000000000 + 100000000000

	return strconv.FormatUint(n, 10)
}

// Now returns the current UTC time formatted as RFC 3339 with nanoseconds,
// the timestamp format used across Google Cloud JSON APIs.
func Now() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

// SplitVerb splits a GCP custom-method path segment into its resource id and
// verb. Google REST APIs append custom methods with a colon, e.g.
// "my-secret:addVersion" or "1:access". When no colon is present, verb is "".
func SplitVerb(segment string) (id, verb string) {
	if i := strings.IndexByte(segment, ':'); i >= 0 {
		return segment[:i], segment[i+1:]
	}

	return segment, ""
}

func reasonToCamel(reason string) string {
	parts := strings.Split(strings.ToLower(reason), "_")
	for i, p := range parts {
		if i == 0 || p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}

	return strings.Join(parts, "")
}
