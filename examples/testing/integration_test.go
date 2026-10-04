package testing_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/Brilhante29/kiri-gcp"
	"google.golang.org/api/option"
)

// TestInProcessKiriServer demonstrates zero-dependency unit and integration
// testing by booting an in-process kiri server on random localhost ports.
func TestInProcessKiriServer(t *testing.T) {
	// 1. Boot in-process kiri emulator
	srv := kiri.NewServer()
	t.Cleanup(func() {
		srv.Close()
	})

	t.Logf("✓ Started in-process kiri server at REST URL: %s", srv.URL)

	ctx := context.Background()

	// 2. Connect official Google Cloud Go SDK
	client, err := storage.NewClient(ctx,
		option.WithEndpoint(srv.URL+"/storage/v1/"),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("failed to create storage client: %v", err)
	}
	t.Cleanup(func() {
		client.Close()
	})

	// 3. Perform GCS operations
	bucketName := "test-suite-bucket"
	bucket := client.Bucket(bucketName)

	if err := bucket.Create(ctx, "test-project", nil); err != nil {
		t.Fatalf("failed to create bucket: %v", err)
	}

	obj := bucket.Object("test-payload.txt")
	writer := obj.NewWriter(ctx)
	content := []byte("Integration test payload content")
	if _, err := writer.Write(content); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close writer: %v", err)
	}

	reader, err := obj.NewReader(ctx)
	if err != nil {
		t.Fatalf("failed to read object back: %v", err)
	}
	defer reader.Close()

	readData, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read data: %v", err)
	}

	if string(readData) != string(content) {
		t.Errorf("expected content %q, got %q", string(content), string(readData))
	}
	t.Log("✓ Successfully validated GCS bucket write/read against in-process kiri server")

	// A small ChunkSize makes the client use a chunked resumable upload,
	// the path it takes for large objects.
	large := bytes.Repeat([]byte("kiri-resumable-"), 70_000) // ~1 MiB
	chunked := bucket.Object("large/payload.bin").NewWriter(ctx)
	chunked.ChunkSize = 256 * 1024
	if _, err := chunked.Write(large); err != nil {
		t.Fatalf("failed to write large payload: %v", err)
	}
	if err := chunked.Close(); err != nil {
		t.Fatalf("failed to finish resumable upload: %v", err)
	}

	largeReader, err := bucket.Object("large/payload.bin").NewReader(ctx)
	if err != nil {
		t.Fatalf("failed to open large payload: %v", err)
	}
	defer largeReader.Close()

	largeData, err := io.ReadAll(largeReader)
	if err != nil {
		t.Fatalf("failed to read large payload: %v", err)
	}
	if !bytes.Equal(largeData, large) {
		t.Errorf("large payload mismatch: wrote %d bytes, read %d", len(large), len(largeData))
	}
	t.Log("✓ Validated a chunked resumable upload with the official client")

	// 4. Test the Cost surface (Cost Explorer analogue).
	costBody := []byte(`{"groupBy":"service"}`)
	resp, err := http.Post(srv.URL+"/kiri/billing/cost", "application/json", bytes.NewReader(costBody))
	if err != nil {
		t.Fatalf("failed to call cost query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 OK from cost query, got %d", resp.StatusCode)
	}
	t.Log("✓ Validated cost query endpoint on in-process kiri server")
}
