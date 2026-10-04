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

	// The Node.js client builds each listed bucket from its id.
	listed, _ := items[0].(map[string]any)
	if listed["id"] != bucketName || listed["kind"] != "storage#bucket" {
		t.Fatalf("expected id %q and kind storage#bucket, got %v", bucketName, listed)
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

func TestGCSMultipartUploadNameInQuery(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "query-name-bucket"
	base := srv.URL + "/storage/v1"
	http.Post(base+"/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	// The metadata part carries no name; ?name= supplies it, as the Node.js
	// client does for simple uploads.
	mp := "--b1\r\nContent-Type: application/json\r\n\r\n{\"contentType\":\"text/plain\"}\r\n" +
		"--b1\r\nContent-Type: text/plain\r\n\r\nquery named\r\n--b1--"

	resp, err := http.Post(
		srv.URL+"/upload/storage/v1/b/"+bucketName+"/o?uploadType=multipart&name=notes%2Fhello.txt",
		"multipart/related; boundary=b1",
		strings.NewReader(mp),
	)
	if err != nil {
		t.Fatalf("multipart upload: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	resp, err = http.Get(base + "/b/" + bucketName + "/o/notes%2Fhello.txt?alt=media")
	if err != nil {
		t.Fatalf("GET after multipart: %v", err)
	}

	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if string(data) != "query named" {
		t.Fatalf("expected %q, got %q", "query named", string(data))
	}
}

// putChunk sends one resumable-upload request and returns the status and the
// persisted Range header.
func putChunk(t *testing.T, sessionURL, contentRange, body string) (int, string) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodPut, sessionURL, strings.NewReader(body))
	req.Header.Set("Content-Range", contentRange)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT %s: %v", contentRange, err)
	}

	resp.Body.Close()

	return resp.StatusCode, resp.Header.Get("Range")
}

func TestGCSResumableUpload(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "resumable-bucket"
	base := srv.URL + "/storage/v1"
	http.Post(base+"/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	resp, err := http.Post(
		srv.URL+"/upload/storage/v1/b/"+bucketName+"/o?uploadType=resumable",
		"application/json",
		strings.NewReader(`{"name":"logs/app.log","contentType":"text/plain"}`),
	)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	resp.Body.Close()
	sessionURL := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusOK || !strings.Contains(sessionURL, "upload_id=") {
		t.Fatalf("expected 200 with a session Location, got %d %q", resp.StatusCode, sessionURL)
	}

	steps := []struct {
		contentRange, body, wantRange string
	}{
		{"bytes 0-4/*", "hello", "bytes=0-4"},
		{"bytes */*", "", "bytes=0-4"},        // status check
		{"bytes 7-9/11", "rld", "bytes=0-4"},  // gap: not applied
		{"bytes 0-4/*", "hello", "bytes=0-4"}, // duplicate: not applied
	}
	for _, step := range steps {
		status, gotRange := putChunk(t, sessionURL, step.contentRange, step.body)
		if status != http.StatusPermanentRedirect || gotRange != step.wantRange {
			t.Fatalf("%s: expected 308 %q, got %d %q", step.contentRange, step.wantRange, status, gotRange)
		}
	}

	req, _ := http.NewRequest(http.MethodPut, sessionURL, strings.NewReader(" world"))
	req.Header.Set("Content-Range", "bytes 5-10/11")

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("final chunk: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on the final chunk, got %d", resp.StatusCode)
	}

	var obj map[string]any
	json.NewDecoder(resp.Body).Decode(&obj)

	if obj["name"] != "logs/app.log" || obj["size"] != "11" {
		t.Fatalf("unexpected object resource: %v", obj)
	}

	// Reference checksums of "hello world".
	if obj["md5Hash"] != "XrY7u+Ae7tCTyyK7j1rNww==" || obj["crc32c"] != "yZRlqg==" {
		t.Fatalf("unexpected checksums: md5Hash=%v crc32c=%v", obj["md5Hash"], obj["crc32c"])
	}

	media, err := http.Get(base + "/b/" + bucketName + "/o/logs%2Fapp.log?alt=media")
	if err != nil {
		t.Fatalf("GET media: %v", err)
	}

	defer media.Body.Close()
	data, _ := io.ReadAll(media.Body)

	if string(data) != "hello world" || media.Header.Get("X-Goog-Hash") != "crc32c=yZRlqg==,md5=XrY7u+Ae7tCTyyK7j1rNww==" {
		t.Fatalf("unexpected media %q with X-Goog-Hash %q", string(data), media.Header.Get("X-Goog-Hash"))
	}

	// The finished session is gone.
	if status, _ := putChunk(t, sessionURL, "bytes */11", ""); status != http.StatusNotFound {
		t.Fatalf("expected 404 for a finished session, got %d", status)
	}
}

func TestGCSResumableUploadNo308(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "no308-bucket"
	http.Post(srv.URL+"/storage/v1/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	resp, err := http.Post(srv.URL+"/upload/storage/v1/b/"+bucketName+"/o?uploadType=resumable&name=chunked.bin", "application/json", nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	resp.Body.Close()
	sessionURL := resp.Header.Get("Location")

	// The Go client POSTs chunks and asks for 200 plus an override header,
	// because a bare 308 reads as a redirect to its HTTP stack.
	post := func(contentRange, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, sessionURL, strings.NewReader(body))
		req.Header.Set("Content-Range", contentRange)
		req.Header.Set("X-GUploader-No-308", "yes")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST %s: %v", contentRange, err)
		}

		resp.Body.Close()

		return resp
	}

	first := post("bytes 0-3/*", "abcd")
	if first.StatusCode != http.StatusOK || first.Header.Get("X-Http-Status-Code-Override") != "308" || first.Header.Get("Range") != "bytes=0-3" {
		t.Fatalf("expected 200 with a 308 override and bytes=0-3, got %d %q %q",
			first.StatusCode, first.Header.Get("X-Http-Status-Code-Override"), first.Header.Get("Range"))
	}

	last := post("bytes 4-5/6", "ef")
	if last.StatusCode != http.StatusOK || last.Header.Get("X-Http-Status-Code-Override") != "" {
		t.Fatalf("expected a plain 200 on the final chunk, got %d %q", last.StatusCode, last.Header.Get("X-Http-Status-Code-Override"))
	}

	media, err := http.Get(srv.URL + "/storage/v1/b/" + bucketName + "/o/chunked.bin?alt=media")
	if err != nil {
		t.Fatalf("GET media: %v", err)
	}

	defer media.Body.Close()
	data, _ := io.ReadAll(media.Body)

	if string(data) != "abcdef" {
		t.Fatalf("expected %q, got %q", "abcdef", string(data))
	}
}

func TestGCSResumableUploadSingleRequest(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "stream-bucket"
	http.Post(srv.URL+"/storage/v1/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	// Name in the query and an empty metadata body, as the Node.js client
	// sends it; then the whole stream in one request of unknown size.
	resp, err := http.Post(srv.URL+"/upload/storage/v1/b/"+bucketName+"/o?uploadType=resumable&name=stream.txt", "application/json", nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	resp.Body.Close()

	if status, _ := putChunk(t, resp.Header.Get("Location"), "bytes 0-*/*", "streamed"); status != http.StatusOK {
		t.Fatalf("expected 200 for a single-request upload, got %d", status)
	}

	media, err := http.Get(srv.URL + "/storage/v1/b/" + bucketName + "/o/stream.txt?alt=media")
	if err != nil {
		t.Fatalf("GET media: %v", err)
	}

	defer media.Body.Close()
	data, _ := io.ReadAll(media.Body)

	if string(data) != "streamed" {
		t.Fatalf("expected %q, got %q", "streamed", string(data))
	}
}

func TestGCSJSONMediaDownloadPath(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	bucketName := "download-bucket"
	http.Post(srv.URL+"/storage/v1/b?project=p", "application/json", strings.NewReader(`{"name":"`+bucketName+`"}`))

	// Nested names are percent-encoded by the clients, as in this upload.
	uploadURL := srv.URL + "/upload/storage/v1/b/" + bucketName + "/o?name=reports%2Fq3.csv&uploadType=media"
	resp, err := http.Post(uploadURL, "text/csv", strings.NewReader("a,b\n1,2\n"))
	if err != nil {
		t.Fatalf("POST upload: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 upload, got %d", resp.StatusCode)
	}

	// The Python client downloads through the JSON API's /download host path.
	resp, err = http.Get(srv.URL + "/download/storage/v1/b/" + bucketName + "/o/reports%2Fq3.csv?alt=media")
	if err != nil {
		t.Fatalf("GET /download: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	data, _ := io.ReadAll(resp.Body)
	if string(data) != "a,b\n1,2\n" {
		t.Fatalf("expected the uploaded bytes, got %q", string(data))
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
