package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestResourceManagerDefaults(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/projects")
	if err != nil {
		t.Fatalf("GET projects: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Projects []map[string]any `json:"projects"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Projects) == 0 {
		t.Fatal("expected at least 1 seeded project")
	}

	found := false
	for _, p := range result.Projects {
		if p["projectId"] == "kiri-project" {
			found = true
			if p["state"] != "ACTIVE" {
				t.Fatalf("expected ACTIVE, got %v", p["state"])
			}
			break
		}
	}

	if !found {
		t.Fatal("expected default project kiri-project")
	}
}

func TestResourceManagerCRUD(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	// Create.
	resp, err := http.Post(srv.URL+"/v1/projects", "application/json", strings.NewReader(`{"projectId":"test-project"}`))
	if err != nil {
		t.Fatalf("POST projects: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Get.
	resp, err = http.Get(srv.URL + "/v1/projects/test-project")
	if err != nil {
		t.Fatalf("GET project: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Update labels.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/projects/test-project/labels", strings.NewReader(`{"labels":{"env":"test"}}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT labels: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Delete.
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/v1/projects/test-project", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE project: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestSecretManager(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	parent := "projects/kiri-project"

	// Create secret.
	body := `{"secretId":"my-secret","labels":{"env":"test"}}`
	resp, err := http.Post(srv.URL+"/v1/"+parent+"/secrets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST secret: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// List secrets.
	resp, err = http.Get(srv.URL + "/v1/" + parent + "/secrets")
	if err != nil {
		t.Fatalf("GET secrets: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Add version.
	secretName := parent + "/secrets/my-secret"
	verBody := `{"payload":{"data":"s3cr3t"}}`
	resp, err = http.Post(srv.URL+"/v1/"+secretName+"/versions", "application/json", strings.NewReader(verBody))
	if err != nil {
		t.Fatalf("POST version: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Access version.
	resp, err = http.Get(srv.URL + "/v1/" + secretName + "/versions/1:access")
	if err != nil {
		t.Fatalf("GET access: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var payload map[string]any
	json.NewDecoder(resp.Body).Decode(&payload)
	p := payload["payload"].(map[string]any)

	if p["data"] != "s3cr3t" {
		t.Fatalf("expected s3cr3t, got %v", p["data"])
	}
}

func TestKMS(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	parent := "projects/kiri-project/locations/us-central1"

	// Create key ring.
	resp, err := http.Post(srv.URL+"/v1/"+parent+"/keyRings?keyRingId=test-kr", "application/json", nil)
	if err != nil {
		t.Fatalf("POST keyRing: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Create crypto key.
	resp, err = http.Post(srv.URL+"/v1/"+parent+"/keyRings/test-kr/cryptoKeys?cryptoKeyId=test-ck", "application/json", nil)
	if err != nil {
		t.Fatalf("POST cryptoKey: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Create version.
	keyName := parent + "/keyRings/test-kr/cryptoKeys/test-ck"
	resp, err = http.Post(srv.URL+"/v1/"+keyName+"/cryptoKeyVersions", "application/json", nil)
	if err != nil {
		t.Fatalf("POST version: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestIAM(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	project := "kiri-project"

	// Create service account.
	body := `{"accountId":"test-sa","displayName":"Test Service Account"}`
	resp, err := http.Post(srv.URL+"/v1/projects/"+project+"/serviceAccounts", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST serviceAccount: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	saEmail := "test-sa@" + project + ".iam.gserviceaccount.com"

	// Get.
	resp, err = http.Get(srv.URL + "/v1/projects/" + project + "/serviceAccounts/" + saEmail)
	if err != nil {
		t.Fatalf("GET serviceAccount: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// List roles.
	resp, err = http.Get(srv.URL + "/v1/roles")
	if err != nil {
		t.Fatalf("GET roles: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var rolesResp struct {
		Roles []map[string]any `json:"roles"`
	}
	json.NewDecoder(resp.Body).Decode(&rolesResp)

	if len(rolesResp.Roles) < 3 {
		t.Fatalf("expected at least 3 roles, got %d", len(rolesResp.Roles))
	}
}

func TestPubSub(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	parent := "projects/kiri-project"

	// Create topic.
	body := `{"topicId":"test-topic"}`
	resp, err := http.Post(srv.URL+"/v1/"+parent+"/topics", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST topic: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// List topics.
	resp, err = http.Get(srv.URL + "/v1/" + parent + "/topics")
	if err != nil {
		t.Fatalf("GET topics: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Create subscription.
	topicName := parent + "/topics/test-topic"
	subBody := `{"topic":"` + topicName + `","subscriptionId":"test-sub"}`
	resp, err = http.Post(srv.URL+"/v1/"+parent+"/subscriptions", "application/json", strings.NewReader(subBody))
	if err != nil {
		t.Fatalf("POST subscription: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Publish message.
	pubBody := `{"messages":[{"data":"hello"}]}`
	resp, err = http.Post(srv.URL+"/v1/"+topicName+":publish", "application/json", strings.NewReader(pubBody))
	if err != nil {
		t.Fatalf("POST publish: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var pubResp struct {
		MessageIDs []string `json:"messageIds"`
	}
	json.NewDecoder(resp.Body).Decode(&pubResp)

	if len(pubResp.MessageIDs) != 1 {
		t.Fatalf("expected 1 messageId, got %d", len(pubResp.MessageIDs))
	}

	// Pull messages.
	subName := parent + "/subscriptions/test-sub"
	resp, err = http.Post(srv.URL+"/v1/"+subName+":pull", "application/json", nil)
	if err != nil {
		t.Fatalf("POST pull: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var pullResp struct {
		Messages []map[string]any `json:"receivedMessages"`
	}
	json.NewDecoder(resp.Body).Decode(&pullResp)

	if len(pullResp.Messages) != 1 {
		t.Fatalf("expected 1 pulled message, got %d", len(pullResp.Messages))
	}

	// Acknowledge.
	ackBody := `{"ackIds":["` + pubResp.MessageIDs[0] + `-ack"]}`
	resp, err = http.Post(srv.URL+"/v1/"+subName+":acknowledge", "application/json", strings.NewReader(ackBody))
	if err != nil {
		t.Fatalf("POST ack: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestServiceUsage(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/projects/kiri-project/services")
	if err != nil {
		t.Fatalf("GET services: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result struct {
		Services []map[string]any `json:"services"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if len(result.Services) == 0 {
		t.Fatal("expected seeded services")
	}
}
