package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	kiriHTTP := "http://localhost:4443"

	fmt.Println("=== kiri Go SDK Integration Example ===")

	// 1. Initialize Cloud Storage client pointing to local kiri emulator
	client, err := storage.NewClient(ctx,
		option.WithEndpoint(kiriHTTP),
		option.WithoutAuthentication(),
	)
	if err != nil {
		log.Fatalf("failed to create storage client: %v", err)
	}
	defer client.Close()

	bucketName := "local-app-bucket"
	bucket := client.Bucket(bucketName)

	// Create bucket
	if err := bucket.Create(ctx, "local-project", nil); err != nil {
		fmt.Printf("Bucket creation (or already exists): %v\n", err)
	} else {
		fmt.Printf("✓ Created GCS bucket: %s\n", bucketName)
	}

	// Upload object
	obj := bucket.Object("hello.txt")
	writer := obj.NewWriter(ctx)
	writer.ContentType = "text/plain"
	if _, err := writer.Write([]byte("Hello, kiri 霧 local GCP emulator!")); err != nil {
		log.Fatalf("failed to write object: %v", err)
	}
	if err := writer.Close(); err != nil {
		log.Fatalf("failed to close writer: %v", err)
	}
	fmt.Println("✓ Uploaded object 'hello.txt' to bucket")

	// Read object back
	reader, err := obj.NewReader(ctx)
	if err != nil {
		log.Fatalf("failed to read object: %v", err)
	}
	defer reader.Close()

	content, _ := io.ReadAll(reader)
	fmt.Printf("✓ Read object content: %s\n", string(content))

	// 2. Query kiri's Cost surface (the Cost Explorer analogue), grouped by service.
	costPayload := []byte(`{"groupBy":"service"}`)

	resp, err := http.Post(kiriHTTP+"/kiri/billing/cost", "application/json", bytes.NewReader(costPayload))
	if err != nil {
		log.Fatalf("failed to query cost: %v", err)
	}
	defer resp.Body.Close()

	costResp, _ := io.ReadAll(resp.Body)
	fmt.Printf("✓ Cost query (grouped by service):\n%s\n", string(costResp))
}
