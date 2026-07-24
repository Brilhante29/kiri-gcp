package cloudfunctions_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Brilhante29/kiri-gcp/internal/server"
	"github.com/Brilhante29/kiri-gcp/internal/service/cloudfunctions"
)

func TestCloudFunctionsService(t *testing.T) {
	svc := cloudfunctions.New()
	if svc.Name() != "cloudfunctions" {
		t.Fatalf("expected name cloudfunctions, got %s", svc.Name())
	}

	cfg := server.DefaultConfig()
	srv := server.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Create Function
	reqBody, _ := json.Marshal(map[string]string{
		"name":        "my-function",
		"description": "test function",
		"runtime":     "go121",
	})
	resp, err := http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/functions", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		t.Fatalf("failed to create function: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 2. Call Function
	resp, err = http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/functions/my-function:call", "application/json", nil)
	if err != nil {
		t.Fatalf("failed to call function: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var callRes struct {
		ExecutionID string `json:"executionId"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&callRes)
	if callRes.ExecutionID == "" {
		t.Fatalf("expected executionId, got empty")
	}

	// 3. Delete Function
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/projects/my-proj/locations/us-central1/functions/my-function", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to delete function: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}
}
