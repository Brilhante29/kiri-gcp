package bigquery_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Brilhante29/kiri-gcp/internal/server"
	"github.com/Brilhante29/kiri-gcp/internal/service/bigquery"
)

func TestBigQueryService(t *testing.T) {
	svc := bigquery.New()
	if svc.Name() != "bigquery" {
		t.Fatalf("expected name bigquery, got %s", svc.Name())
	}

	cfg := server.DefaultConfig()
	srv := server.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Create Dataset
	dsReq, _ := json.Marshal(map[string]any{
		"datasetReference": map[string]string{
			"datasetId": "my_dataset",
		},
		"location": "US",
	})
	resp, err := http.Post(ts.URL+"/bigquery/v2/projects/my-proj/datasets", "application/json", bytes.NewReader(dsReq))
	if err != nil {
		t.Fatalf("failed to create dataset: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 2. Create Table
	tblReq, _ := json.Marshal(map[string]any{
		"tableReference": map[string]string{
			"tableId": "my_table",
		},
	})
	resp, err = http.Post(ts.URL+"/bigquery/v2/projects/my-proj/datasets/my_dataset/tables", "application/json", bytes.NewReader(tblReq))
	if err != nil {
		t.Fatalf("failed to create table: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 3. Query
	qReq, _ := json.Marshal(map[string]string{"query": "SELECT 1"})
	resp, err = http.Post(ts.URL+"/bigquery/v2/projects/my-proj/queries", "application/json", bytes.NewReader(qReq))
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	var qRes struct {
		Kind string `json:"kind"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&qRes)
	if qRes.Kind != "bigquery#queryResponse" {
		t.Fatalf("expected bigquery#queryResponse, got %s", qRes.Kind)
	}
}
