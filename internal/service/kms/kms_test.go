package kms_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kiri-dev/kiri/internal/server"
	"github.com/kiri-dev/kiri/internal/service/kms"
)

func TestKMSService(t *testing.T) {
	svc := kms.New()
	if svc.Name() != "kms" {
		t.Fatalf("expected service name kms, got %s", svc.Name())
	}

	cfg := server.DefaultConfig()
	srv := server.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 1. Create KeyRing
	resp, err := http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/keyRings?keyRingId=my-ring", "application/json", nil)
	if err != nil {
		t.Fatalf("failed to create keyring: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var kr struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&kr)
	expectedKRName := "projects/my-proj/locations/us-central1/keyRings/my-ring"
	if kr.Name != expectedKRName {
		t.Fatalf("expected keyring name %s, got %s", expectedKRName, kr.Name)
	}

	// 2. Create CryptoKey
	body, _ := json.Marshal(map[string]string{"purpose": "ENCRYPT_DECRYPT"})
	resp, err = http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/keyRings/my-ring/cryptoKeys?cryptoKeyId=my-key", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create cryptokey: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	// 3. Encrypt
	encReq, _ := json.Marshal(map[string]string{"plaintext": "hello world"})
	resp, err = http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key:encrypt", "application/json", bytes.NewReader(encReq))
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}
	var encRes struct {
		Ciphertext string `json:"ciphertext"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&encRes)
	if encRes.Ciphertext == "" {
		t.Fatalf("expected ciphertext, got empty")
	}

	// 4. Decrypt
	decReq, _ := json.Marshal(map[string]string{"ciphertext": encRes.Ciphertext})
	resp, err = http.Post(ts.URL+"/v1/projects/my-proj/locations/us-central1/keyRings/my-ring/cryptoKeys/my-key:decrypt", "application/json", bytes.NewReader(decReq))
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}
	var decRes struct {
		Plaintext string `json:"plaintext"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&decRes)
	if decRes.Plaintext == "" {
		t.Fatalf("expected plaintext, got empty")
	}
}
