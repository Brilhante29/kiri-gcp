package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFirestoreGetNonExistent(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	// Two-segment path: collection/document
	resp, err := http.Get(srv.URL + "/v1/projects/p/databases/(default)/documents/users/nonexistent")
	if err != nil {
		t.Fatalf("GET document: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", resp.StatusCode)
	}
}

func TestFirestoreCreateAndGetDocument(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/projects/myproject/databases/(default)/documents"

	// Create document in collection "users".
	body := `{"document":{"fields":{"name":{"stringValue":"Alice"}}}}`
	resp, err := http.Post(base+"/users?documentId=alice", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST document: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var doc map[string]any
	json.NewDecoder(resp.Body).Decode(&doc)

	expectedName := "projects/myproject/databases/(default)/documents/users/alice"
	if doc["name"] != expectedName {
		t.Fatalf("expected name %q, got %q", expectedName, doc["name"])
	}

	// Get document.
	resp, err = http.Get(base + "/users/alice")
	if err != nil {
		t.Fatalf("GET document: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestFirestoreListDocuments(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/projects/p/databases/(default)/documents"

	// Create two documents in collection "items".
	for _, id := range []string{"item1", "item2"} {
		resp, err := http.Post(base+"/items?documentId="+id, "application/json", strings.NewReader(`{"document":{}}`))
		if err != nil {
			t.Fatalf("POST %s: %v", id, err)
		}
		resp.Body.Close()
	}

	// List documents (single segment path = collection name).
	resp, err := http.Get(base + "/items")
	if err != nil {
		t.Fatalf("GET documents list: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var list struct {
		Documents []map[string]any `json:"documents"`
	}
	json.NewDecoder(resp.Body).Decode(&list)

	if len(list.Documents) != 2 {
		t.Fatalf("expected 2 documents, got %d", len(list.Documents))
	}
}

func TestFirestoreCommitTransaction(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/projects/p/databases/(default)/documents"

	// Begin transaction.
	resp, err := http.Post(base+":beginTransaction", "application/json", nil)
	if err != nil {
		t.Fatalf("POST beginTransaction: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var txResp map[string]any
	json.NewDecoder(resp.Body).Decode(&txResp)

	if _, ok := txResp["transaction"]; !ok {
		t.Fatal("expected transaction field")
	}

	// Commit.
	resp, err = http.Post(base+":commit", "application/json", nil)
	if err != nil {
		t.Fatalf("POST commit: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestFirestoreUpdateDocument(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/projects/p/databases/(default)/documents"

	// Create a document.
	http.Post(base+"/upd-test?documentId=target", "application/json", strings.NewReader(`{"document":{"fields":{"x":{"stringValue":"old"}}}}`))

	// Update via PATCH.
	body := `{"document":{"fields":{"y":{"stringValue":"new"}}}}`
	req, _ := http.NewRequest(http.MethodPatch, base+"/upd-test/target", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH document: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var doc map[string]any
	json.NewDecoder(resp.Body).Decode(&doc)
	fields := doc["fields"].(map[string]any)

	if _, ok := fields["y"]; !ok {
		t.Fatal("expected field y after update")
	}
}

func TestFirestoreDeleteDocument(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/projects/p/databases/(default)/documents"

	// Create.
	http.Post(base+"/del-test?documentId=to-delete", "application/json", strings.NewReader(`{"document":{}}`))

	// Delete.
	req, _ := http.NewRequest(http.MethodDelete, base+"/del-test/to-delete", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE document: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}

	// Confirm deleted.
	resp, err = http.Get(base + "/del-test/to-delete")
	if err != nil {
		t.Fatalf("GET deleted document: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 after delete, got %d", resp.StatusCode)
	}
}
