package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestGCSBucketCRUD(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/storage/v1"

	// Create bucket.
	bucketName := "test-bucket-1"
	body := `{"name":"` + bucketName + `"}`
	resp, err := http.Post(base+"/b?project=test-project", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /b: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var created map[string]any
	json.NewDecoder(resp.Body).Decode(&created)

	if created["name"] != bucketName {
		t.Fatalf("expected name %q, got %q", bucketName, created["name"])
	}

	// List buckets.
	resp, err = http.Get(base + "/b")
	if err != nil {
		t.Fatalf("GET /b: %v", err)
	}

	defer resp.Body.Close()
	var list map[string]any
	json.NewDecoder(resp.Body).Decode(&list)

	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 bucket, got %d", len(items))
	}

	// Get bucket.
	resp, err = http.Get(base + "/b/" + bucketName)
	if err != nil {
		t.Fatalf("GET /b/%s: %v", bucketName, err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Delete bucket.
	req, _ := http.NewRequest(http.MethodDelete, base+"/b/"+bucketName, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /b/%s: %v", bucketName, err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestGCSObjectCRUD(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/storage/v1"

	// Create bucket first.
	bucketName := "obj-test-bucket"
	http.Post(base+"/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	// Upload object via media upload.
	objName := "my-object.txt"
	objData := "hello world"
	uploadURL := srv.URL + "/upload/storage/v1/b/" + bucketName + "/o?name=" + objName + "&uploadType=media"
	resp, err := http.Post(uploadURL, "text/plain", strings.NewReader(objData))
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 upload, got %d", resp.StatusCode)
	}

	// Get object metadata.
	resp, err = http.Get(base + "/b/" + bucketName + "/o/" + objName)
	if err != nil {
		t.Fatalf("GET /o: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var obj map[string]any
	json.NewDecoder(resp.Body).Decode(&obj)

	if obj["name"] != objName {
		t.Fatalf("expected name %q, got %q", objName, obj["name"])
	}

	// Download object bytes (alt=media).
	resp, err = http.Get(base + "/b/" + bucketName + "/o/" + objName + "?alt=media")
	if err != nil {
		t.Fatalf("GET /o?alt=media: %v", err)
	}

	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if string(data) != objData {
		t.Fatalf("expected body %q, got %q", objData, string(data))
	}

	// List objects.
	resp, err = http.Get(base + "/b/" + bucketName + "/o")
	if err != nil {
		t.Fatalf("GET /o list: %v", err)
	}

	defer resp.Body.Close()
	var list map[string]any
	json.NewDecoder(resp.Body).Decode(&list)

	items, _ := list["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 object, got %d", len(items))
	}

	// Delete object.
	req, _ := http.NewRequest(http.MethodDelete, base+"/b/"+bucketName+"/o/"+objName, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /o: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestGCSMultipartUpload(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "multipart-bucket"
	base := srv.URL + "/storage/v1"
	http.Post(base+"/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	// Build multipart body.
	mp := `--boundary123
Content-Type: application/json

{"name":"multi-object.txt","contentType":"text/plain"}
--boundary123
Content-Type: text/plain

multipart content
--boundary123--`

	resp, err := http.Post(
		srv.URL+"/upload/storage/v1/b/"+bucketName+"/o?uploadType=multipart",
		"multipart/related; boundary=boundary123",
		strings.NewReader(mp),
	)
	if err != nil {
		t.Fatalf("multipart upload: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify uploaded object.
	resp, err = http.Get(base + "/b/" + bucketName + "/o/multi-object.txt?alt=media")
	if err != nil {
		t.Fatalf("GET after multipart: %v", err)
	}

	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if string(data) != "multipart content" {
		t.Fatalf("expected %q, got %q", "multipart content", string(data))
	}
}

func TestGCSNotFound(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	// Non-existent bucket.
	resp, err := http.Get(srv.URL + "/storage/v1/b/no-such-bucket")
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing bucket, got %d", resp.StatusCode)
	}
}
