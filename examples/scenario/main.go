// Command kiriscenario provisions and exercises a small but realistic GCP
// architecture against kiri using the REAL, unmodified Google Cloud Go
// client libraries (cloud.google.com/go/storage, cloud.google.com/go/pubsub)
// plus the real REST wire format for Compute Engine and Cloud Billing — the
// exact calls a production app / gcloud would make, only the endpoint changes.
//
// Architecture: an "order ingestion pipeline"
//
//	order file ──Storage SDK──▶ GCS bucket "orders-inbox"
//	order event ─Pub/Sub SDK──▶ topic "order-events" ──▶ sub "order-processor"
//	                            Compute Engine VM "order-worker" (n1-standard-2)
//	                            Cloud Billing: link, PREDICT monthly cost, budget
//
// The billing step pulls live unit prices from the emulator's pricing catalog
// and projects the monthly spend of the resources it just provisioned, then
// checks that projection against a budget — GCP's Cost-Explorer analogue.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
)

const (
	projectID = "shop-prod"
	zone      = "us-central1-a"
	bucket    = "orders-inbox"
	topicName = "order-events"
	subName   = "order-processor"
	workerVM  = "order-worker"
	machine   = "n1-standard-2" // 2 vCPU, 7.5 GiB RAM
)

var (
	gcsEndpoint  = envOr("GCS_ENDPOINT", "http://localhost:4443/storage/v1/")
	restEndpoint = envOr("REST_ENDPOINT", "http://localhost:4443")
	failures     int
)

func main() {
	ctx := context.Background()

	banner("kiri ARCHITECTURE SCENARIO — Order Ingestion Pipeline")
	fmt.Printf("project=%s  zone=%s  REST=%s\n\n", projectID, zone, restEndpoint)

	orderIDs := []string{"ORD-1001", "ORD-1002", "ORD-1003"}

	step("1. Provision object storage (real cloud.google.com/go/storage)")
	stClient := mustStorage(ctx)
	defer stClient.Close()
	provisionStorage(ctx, stClient, orderIDs)

	step("2. Provision messaging (real cloud.google.com/go/pubsub)")
	psClient, topic, sub := provisionPubSub(ctx)
	defer psClient.Close()

	step("3. Provision compute worker (Compute Engine REST)")
	provisionWorker()

	step("4. Run workload — publish order events, worker consumes via streaming pull")
	runWorkload(ctx, topic, sub, orderIDs)

	step("5. Billing — link account, PREDICT monthly cost from live catalog, set budget")
	predictBilling()

	step("6. Behaviors — 404s, list scoping, instance stop/start lifecycle")
	checkBehaviors(ctx, stClient)

	fmt.Println()
	if failures == 0 {
		banner("SCENARIO PASSED — every layer provisioned, exercised, and billed via real clients")
	} else {
		banner(fmt.Sprintf("SCENARIO FAILED — %d assertion(s) did not hold", failures))
		os.Exit(1)
	}
}

// ---------- Step 1: Storage ----------

func provisionStorage(ctx context.Context, c *storage.Client, orderIDs []string) {
	b := c.Bucket(bucket)
	if err := b.Create(ctx, projectID, nil); err != nil {
		// tolerate re-runs against a persisted volume
		fmt.Printf("   note: bucket.Create: %v (continuing — may already exist)\n", err)
	} else {
		ok("bucket %q created", bucket)
	}

	for _, id := range orderIDs {
		payload := fmt.Sprintf(`{"orderId":%q,"item":"widget","qty":3,"total":29.97}`, id)
		w := b.Object(id + ".json").NewWriter(ctx)
		if _, err := io.WriteString(w, payload); err != nil {
			fail("write %s: %v", id, err)
			continue
		}
		if err := w.Close(); err != nil {
			fail("close %s: %v", id, err)
			continue
		}
	}
	ok("uploaded %d order files", len(orderIDs))

	// read one back and verify content round-trips
	r, err := b.Object(orderIDs[0] + ".json").NewReader(ctx)
	if err != nil {
		fail("reader %s: %v", orderIDs[0], err)
		return
	}
	data, _ := io.ReadAll(r)
	r.Close()
	assert(len(data) > 0 && bytes.Contains(data, []byte(orderIDs[0])),
		"read-back of %s.json contains its orderId", orderIDs[0])

	// list must show all uploaded objects
	n := 0
	it := b.Objects(ctx, nil)
	for {
		_, err := it.Next()
		if err != nil {
			break
		}
		n++
	}
	assert(n >= len(orderIDs), "bucket lists >= %d objects (got %d)", len(orderIDs), n)
}

// ---------- Step 2: Pub/Sub ----------

func provisionPubSub(ctx context.Context) (*pubsub.Client, *pubsub.Topic, *pubsub.Subscription) {
	c, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("pubsub.NewClient: %v (PUBSUB_EMULATOR_HOST set?)", err)
	}

	topic, err := c.CreateTopic(ctx, topicName)
	if err != nil {
		topic = c.Topic(topicName) // already exists on a persisted volume
		ok("topic %q (already existed)", topicName)
	} else {
		ok("topic %q created", topicName)
	}

	sub, err := c.CreateSubscription(ctx, subName, pubsub.SubscriptionConfig{Topic: topic})
	if err != nil {
		sub = c.Subscription(subName)
		ok("subscription %q (already existed)", subName)
	} else {
		ok("subscription %q created", subName)
	}
	return c, topic, sub
}

// ---------- Step 3: Compute worker ----------

func provisionWorker() {
	body := map[string]any{
		"name":        workerVM,
		"machineType": "zones/" + zone + "/machineTypes/" + machine,
	}
	var got map[string]any
	code := restCall("POST",
		fmt.Sprintf("/compute/v1/projects/%s/zones/%s/instances", projectID, zone),
		body, &got)
	if code == http.StatusConflict {
		ok("worker VM %q (already existed)", workerVM)
	} else {
		assert(code == 200, "create worker VM returns 200 (got %d)", code)
		assert(got["status"] == "RUNNING", "worker VM status RUNNING (got %v)", got["status"])
	}
}

// ---------- Step 4: Workload ----------

func runWorkload(ctx context.Context, topic *pubsub.Topic, sub *pubsub.Subscription, orderIDs []string) {
	for _, id := range orderIDs {
		res := topic.Publish(ctx, &pubsub.Message{
			Data:       []byte("process:" + id),
			Attributes: map[string]string{"orderId": id, "priority": "standard"},
		})
		if _, err := res.Get(ctx); err != nil {
			fail("publish %s: %v", id, err)
		}
	}
	ok("published %d order events", len(orderIDs))

	// Worker consumes via real streaming pull; collect until we've seen all or time out.
	want := map[string]bool{}
	for _, id := range orderIDs {
		want[id] = true
	}
	got := map[string]bool{}

	rctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()

	_ = sub.Receive(rctx, func(_ context.Context, m *pubsub.Message) {
		if oid := m.Attributes["orderId"]; oid != "" {
			got[oid] = true
		}
		m.Ack()
		if len(got) >= len(want) {
			cancel()
		}
	})

	missing := 0
	for id := range want {
		if !got[id] {
			missing++
		}
	}
	assert(missing == 0, "worker received & acked all %d events (missing %d)", len(want), missing)
	assert(len(got) > 0, "at least one event carried its orderId attribute intact")
}

// ---------- Step 5: Billing prediction ----------

func predictBilling() {
	// Link the project to the seeded billing account.
	acct := "billingAccounts/000000-000000-000000"
	var info map[string]any
	restCall("PUT", "/v1/projects/"+projectID+"/billingInfo",
		map[string]any{"billingAccountName": acct}, &info)
	assert(info["billingEnabled"] == true, "project billing enabled after link")

	// Pull LIVE unit prices from the emulator's pricing catalog — the prediction
	// is driven by the emulator's own data, not hardcoded numbers.
	cpuPrice := skuUnitPrice("6F81-5844-456A", "N1 Predefined vCPU running") // per vCPU-hour
	ramPrice := skuUnitPrice("6F81-5844-456A", "N1 Predefined RAM running")  // per GiB-hour
	gcsPrice := skuUnitPrice("95FF-2EF5-5EA1", "Standard Storage US")        // per GiB-month
	assert(cpuPrice > 0 && ramPrice > 0 && gcsPrice > 0,
		"fetched live SKU prices: vCPU=$%.6f/h RAM=$%.6f/GiB.h GCS=$%.3f/GiB.mo", cpuPrice, ramPrice, gcsPrice)

	// Project one month (730h) of the n1-standard-2 worker + ~5 GiB of orders.
	const hoursPerMonth = 730.0
	const vCPUs = 2.0
	const ramGiB = 7.5
	const storedGiB = 5.0

	computeCost := (vCPUs*cpuPrice + ramGiB*ramPrice) * hoursPerMonth
	storageCost := storedGiB * gcsPrice
	projected := computeCost + storageCost

	fmt.Printf("   predicted monthly cost:\n")
	fmt.Printf("     Compute Engine  %s (%.0fh)  = $%8.2f\n", machine, hoursPerMonth, computeCost)
	fmt.Printf("     Cloud Storage   %.0f GiB-mo         = $%8.2f\n", storedGiB, storageCost)
	fmt.Printf("     ------------------------------------------\n")
	fmt.Printf("     projected total                = $%8.2f / month\n", projected)

	// Seed the projection as detailed cost line items, then query the cost
	// surface (Cost-Explorer analogue) grouped by service and confirm it agrees.
	today := time.Now().UTC()
	start := today.Format("2006-01-02")
	end := today.AddDate(0, 1, 0).Format("2006-01-02")
	seed := []map[string]any{
		{"service": "Compute Engine", "sku": machine, "project": projectID, "cost": round2(computeCost), "usageStart": start, "usageEnd": end},
		{"service": "Cloud Storage", "sku": "Standard Storage US", "project": projectID, "cost": round2(storageCost), "usageStart": start, "usageEnd": end},
	}
	var seedResp map[string]any
	restCall("POST", "/kiri/billing/seed", seed, &seedResp)

	var costResp struct {
		Total  float64          `json:"total"`
		Groups []map[string]any `json:"groups"`
	}
	restCall("POST", "/kiri/billing/cost", map[string]any{
		"start": start, "end": end, "groupBy": "service",
		"filter": map[string]any{"project": projectID},
	}, &costResp)
	assert(nearly(costResp.Total, round2(projected)),
		"cost query total $%.2f matches projection $%.2f", costResp.Total, round2(projected))
	assert(len(costResp.Groups) == 2, "cost query returns 2 service groups (got %d)", len(costResp.Groups))

	// Set a monthly budget and evaluate the projection against it (behavior a
	// real FinOps setup would rely on).
	budgetAmount := 200.0
	var budget map[string]any
	restCall("POST", "/v1/"+acct+"/budgets", map[string]any{
		"displayName": "shop-prod monthly",
		"amount":      map[string]any{"specifiedAmount": map[string]any{"currencyCode": "USD", "units": int64(budgetAmount)}},
		"thresholdRules": []map[string]any{
			{"thresholdPercent": 0.8}, {"thresholdPercent": 1.0},
		},
	}, &budget)
	assert(budget["name"] != nil, "budget created with a resource name")

	pctOfBudget := projected / budgetAmount * 100
	within := projected <= budgetAmount
	assert(within, "projected spend $%.2f is within the $%.0f budget (%.1f%%)", projected, budgetAmount, pctOfBudget)
	if pctOfBudget >= 80 {
		fmt.Printf("   ⚠ projection at %.1f%% of budget — would trip the 80%% threshold alert\n", pctOfBudget)
	} else {
		fmt.Printf("   budget headroom: projection is %.1f%% of the $%.0f budget\n", pctOfBudget, budgetAmount)
	}
}

// ---------- Step 6: Behaviors ----------

func checkBehaviors(ctx context.Context, st *storage.Client) {
	// 404 on a missing object read.
	_, err := st.Bucket(bucket).Object("does-not-exist.json").NewReader(ctx)
	assert(err != nil, "reading a missing object returns an error (not silent empty)")

	// Missing billing account → 404.
	code := restCall("GET", "/v1/billingAccounts/no-such-account", nil, nil)
	assert(code == 404, "GET unknown billing account returns 404 (got %d)", code)

	// Instance lifecycle: stop then start, observe status transitions.
	var stopped, started map[string]any
	restCall("POST", fmt.Sprintf("/compute/v1/projects/%s/zones/%s/instances/%s:stop", projectID, zone, workerVM), map[string]any{}, &stopped)
	restCall("POST", fmt.Sprintf("/compute/v1/projects/%s/zones/%s/instances/%s:start", projectID, zone, workerVM), map[string]any{}, &started)
	assert(fmt.Sprint(stopped["status"]) == "TERMINATED" || fmt.Sprint(stopped["status"]) == "STOPPING",
		"worker VM reports stopped status after :stop (got %v)", stopped["status"])
	assert(fmt.Sprint(started["status"]) == "RUNNING",
		"worker VM back to RUNNING after :start (got %v)", started["status"])

	// List scoping: the worker VM shows up in its zone's instance list.
	var list struct {
		Items []map[string]any `json:"items"`
	}
	restCall("GET", fmt.Sprintf("/compute/v1/projects/%s/zones/%s/instances", projectID, zone), nil, &list)
	found := false
	for _, i := range list.Items {
		if fmt.Sprint(i["name"]) == workerVM || bytes.Contains([]byte(fmt.Sprint(i["name"])), []byte(workerVM)) {
			found = true
		}
	}
	assert(found, "worker VM appears in its zone instance list")
}

// ---------- helpers ----------

func mustStorage(ctx context.Context) *storage.Client {
	c, err := storage.NewClient(ctx, option.WithEndpoint(gcsEndpoint), option.WithoutAuthentication())
	if err != nil {
		log.Fatalf("storage.NewClient: %v", err)
	}
	return c
}

func skuUnitPrice(serviceID, wantDesc string) float64 {
	var resp struct {
		SKUs []struct {
			Description string `json:"description"`
			PricingInfo []struct {
				PricingExpression struct {
					TieredRates []struct {
						UnitPrice struct {
							Units int64 `json:"units"`
							Nanos int64 `json:"nanos"`
						} `json:"unitPrice"`
					} `json:"tieredRates"`
				} `json:"pricingExpression"`
			} `json:"pricingInfo"`
		} `json:"skus"`
	}
	restCall("GET", "/v1/services/"+serviceID+"/skus", nil, &resp)
	for _, k := range resp.SKUs {
		if k.Description == wantDesc && len(k.PricingInfo) > 0 && len(k.PricingInfo[0].PricingExpression.TieredRates) > 0 {
			up := k.PricingInfo[0].PricingExpression.TieredRates[0].UnitPrice
			return float64(up.Units) + float64(up.Nanos)/1e9
		}
	}
	return 0
}

// restCall performs an HTTP JSON request against the emulator and, when out is
// non-nil and the response is 2xx, decodes the body into it. Returns status.
func restCall(method, path string, body, out any) int {
	var rdr io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, restEndpoint+path, rdr)
	if err != nil {
		fail("build %s %s: %v", method, path, err)
		return 0
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fail("%s %s: %v", method, path, err)
		return 0
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode/100 == 2 {
		_ = json.Unmarshal(data, out)
	}
	return resp.StatusCode
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func round2(f float64) float64 { return float64(int64(f*100+0.5)) / 100 }
func nearly(a, b float64) bool  { d := a - b; return d < 0.02 && d > -0.02 }

func banner(s string) { fmt.Printf("======== %s ========\n", s) }
func step(s string)   { fmt.Printf("\n── STEP %s\n", s) }

func ok(f string, a ...any)  { fmt.Printf("   ✓ "+f+"\n", a...) }
func fail(f string, a ...any) {
	failures++
	fmt.Printf("   ✗ "+f+"\n", a...)
}
func assert(cond bool, f string, a ...any) {
	if cond {
		fmt.Printf("   ✓ "+f+"\n", a...)
	} else {
		failures++
		fmt.Printf("   ✗ FAILED: "+f+"\n", a...)
	}
}
