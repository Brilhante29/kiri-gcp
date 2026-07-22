package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestBillingAccounts(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1"

	// List accounts.
	resp, err := http.Get(base + "/billingAccounts")
	if err != nil {
		t.Fatalf("GET billingAccounts: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var listResp struct {
		Accounts []map[string]any `json:"billingAccounts"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Accounts) == 0 {
		t.Fatal("expected at least 1 billing account (default seeded)")
	}

	// Get specific account.
	acctName := listResp.Accounts[0]["name"].(string)
	resp, err = http.Get(base + "/billingAccounts/" + strings.TrimPrefix(acctName, "billingAccounts/"))
	if err != nil {
		t.Fatalf("GET billingAccounts/{id}: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var acct map[string]any
	json.NewDecoder(resp.Body).Decode(&acct)

	if acct["name"] != acctName {
		t.Fatalf("expected name %q, got %q", acctName, acct["name"])
	}
}

func TestBillingCreateAccount(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/billingAccounts", "application/json", strings.NewReader(`{"displayName":"Test Account"}`))
	if err != nil {
		t.Fatalf("POST billingAccounts: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var acct map[string]any
	json.NewDecoder(resp.Body).Decode(&acct)

	if acct["displayName"] != "Test Account" {
		t.Fatalf("expected displayName %q, got %q", "Test Account", acct["displayName"])
	}

	if acct["open"] != true {
		t.Fatal("new account should be open")
	}
}

func TestBillingInfo(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	project := "my-project"
	base := srv.URL + "/v1/projects/" + project

	// Initially no billing attached.
	resp, err := http.Get(base + "/billingInfo")
	if err != nil {
		t.Fatalf("GET billingInfo: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var info map[string]any
	json.NewDecoder(resp.Body).Decode(&info)

	if info["billingEnabled"] != false {
		t.Fatal("expected billingEnabled=false initially")
	}

	// Update billing info.
	acctName := "billingAccounts/TEST-123"
	body := `{"billingAccountName":"` + acctName + `"}`
	req, _ := http.NewRequest(http.MethodPut, base+"/billingInfo", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT billingInfo: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	json.NewDecoder(resp.Body).Decode(&info)

	if info["billingAccountName"] != acctName {
		t.Fatalf("expected billingAccountName %q, got %q", acctName, info["billingAccountName"])
	}

	if info["billingEnabled"] != true {
		t.Fatal("expected billingEnabled=true after update")
	}
}

func TestPricingCatalog(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1"

	// List services.
	resp, err := http.Get(base + "/services")
	if err != nil {
		t.Fatalf("GET services: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var svcResp struct {
		Services []map[string]any `json:"services"`
	}
	json.NewDecoder(resp.Body).Decode(&svcResp)

	if len(svcResp.Services) == 0 {
		t.Fatal("expected seeded services")
	}

	// Get SKUs for first service.
	svcID := svcResp.Services[0]["serviceId"].(string)
	resp, err = http.Get(base + "/services/" + svcID + "/skus")
	if err != nil {
		t.Fatalf("GET services/%s/skus: %v", svcID, err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var skuResp struct {
		SKUs []map[string]any `json:"skus"`
	}
	json.NewDecoder(resp.Body).Decode(&skuResp)

	if len(skuResp.SKUs) == 0 {
		t.Fatal("expected seeded SKUs")
	}
}

func TestBudgets(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	base := srv.URL + "/v1/billingAccounts/000000-000000-000000"

	// Create budget.
	body := `{"displayName":"Monthly Budget","amount":{"specifiedAmount":{"currencyCode":"USD","units":100}}}`
	resp, err := http.Post(base+"/budgets", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST budgets: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var bgt map[string]any
	json.NewDecoder(resp.Body).Decode(&bgt)

	if bgt["displayName"] != "Monthly Budget" {
		t.Fatalf("expected displayName %q, got %q", "Monthly Budget", bgt["displayName"])
	}

	// List budgets.
	resp, err = http.Get(base + "/budgets")
	if err != nil {
		t.Fatalf("GET budgets: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var listResp struct {
		Budgets []map[string]any `json:"budgets"`
	}
	json.NewDecoder(resp.Body).Decode(&listResp)

	if len(listResp.Budgets) != 1 {
		t.Fatalf("expected 1 budget, got %d", len(listResp.Budgets))
	}

	// Delete budget.
	budgetName := bgt["name"].(string)
	parts := strings.Split(budgetName, "/")
	budgetID := parts[len(parts)-1]

	req, _ := http.NewRequest(http.MethodDelete, base+"/budgets/"+budgetID, nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE budget: %v", err)
	}

	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestCostQuery(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	// Cost query without filters should return seeded data.
	resp, err := http.Post(srv.URL+"/kiri/billing/cost", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST cost: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if result["total"].(float64) <= 0 {
		t.Fatalf("expected total>0 from seeded data, got %v", result["total"])
	}

	groups, _ := result["groups"].([]any)
	if len(groups) == 0 {
		t.Fatal("expected at least 1 group from seeded data")
	}

	// Query with groupBy=project.
	resp, err = http.Post(srv.URL+"/kiri/billing/cost", "application/json", strings.NewReader(`{"groupBy":"project"}`))
	if err != nil {
		t.Fatalf("POST cost groupBy=project: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	json.NewDecoder(resp.Body).Decode(&result)

	if result["groupBy"] != "project" {
		t.Fatalf("expected groupBy=project, got %v", result["groupBy"])
	}
}

func TestCostSeed(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	body := `[{"service":"Custom","sku":"Custom SKU","project":"seed-test","cost":99.99,"usageStart":"2026-07-01","usageEnd":"2026-07-20"}]`
	resp, err := http.Post(srv.URL+"/kiri/billing/seed", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST seed: %v", err)
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)

	if result["added"].(float64) != 1 {
		t.Fatalf("expected added=1, got %v", result["added"])
	}
}
