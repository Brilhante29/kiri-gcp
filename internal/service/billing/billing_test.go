package billing_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Brilhante29/kiri/internal/server"
	"github.com/Brilhante29/kiri/internal/service/billing"
)

func TestBillingServicePriceCalculatorScenario(t *testing.T) {
	svc := billing.New()
	if svc.Name() != "billing" {
		t.Fatalf("expected service name billing, got %s", svc.Name())
	}

	cfg := server.DefaultConfig()
	srv := server.New(cfg)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Scenario Test:
	// 1. VM: 4 vCPUs, 7 GB RAM, 9h ON/day (270h/month), 100 GB PD Storage
	// 2. Cloud Run Jobs: 100 tasks of 300s duration (30,000 sec total execution) with 2 vCPUs, 4 GB RAM
	// 3. Vertex AI: 50 Custom Training Node Hours with 1 GPU T4, plus 1,000,000 input & 500,000 output tokens
	calcReq, _ := json.Marshal(map[string]any{
		"items": []map[string]any{
			{
				"service":      "vm",
				"vcpus":        4.0,
				"memoryGiB":    7.0,
				"dailyOnHours": 9.0, // 9h/day * 30 days = 270h/month
				"storageGiB":   100.0,
			},
			{
				"service":             "cloudrunjobs",
				"vcpus":               2.0,
				"memoryGiB":           4.0,
				"taskCount":           100,
				"taskDurationSeconds": 300.0, // 30,000 total seconds
			},
			{
				"service":      "vertexai",
				"nodeHours":    50.0,
				"gpuCount":     1,
				"inputTokens":  1000.0, // 1,000 k-tokens (1M tokens)
				"outputTokens": 500.0,  // 500 k-tokens (500k tokens)
			},
		},
	})

	resp, err := http.Post(ts.URL+"/kiri/billing/calculator", "application/json", bytes.NewReader(calcReq))
	if err != nil {
		t.Fatalf("failed to execute calculator request: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var calcResult struct {
		Currency     string  `json:"currency"`
		MonthlyTotal float64 `json:"monthlyTotal"`
		LineItems    []struct {
			SKUDescription string  `json:"skuDescription"`
			Formula        string  `json:"formula"`
			UnitPrice      float64 `json:"unitPrice"`
			UsageAmount    float64 `json:"usageAmount"`
			Unit           string  `json:"unit"`
			MonthlyCost    float64 `json:"monthlyCost"`
		} `json:"lineItems"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&calcResult); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if calcResult.Currency != "USD" {
		t.Fatalf("expected currency USD, got %s", calcResult.Currency)
	}

	t.Logf("Total Monthly Cost Estimate: $%f USD", calcResult.MonthlyTotal)
	for i, item := range calcResult.LineItems {
		t.Logf("Item #%d: %s | Formula: %s | Cost: $%f", i+1, item.SKUDescription, item.Formula, item.MonthlyCost)
	}

	if calcResult.MonthlyTotal <= 0 {
		t.Fatalf("expected positive monthly total, got %f", calcResult.MonthlyTotal)
	}
}
