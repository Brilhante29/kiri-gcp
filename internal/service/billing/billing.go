// Package billing emulates GCP's cost and billing surface — the closest
// analogue to AWS Cost Explorer. It covers five cooperating surfaces:
//
//  1. Cloud Billing (cloudbilling.googleapis.com/v1): billing accounts, project
//     billing info, and the official GCP pricing catalog (services + SKUs).
//  2. Billing Budgets (billingbudgets.googleapis.com/v1): budgets CRUD.
//  3. Cost query (/kiri/billing/cost): line items grouped by service/sku/project
//     over a time window — the GetCostAndUsage analogue.
//  4. Detailed export seeding (/kiri/billing/seed): inject cost line items.
//  5. GCP Price Calculator (/kiri/billing/calculator): calculate exact monthly
//     estimated costs based on official GCP SKU pricing rules, supporting custom
//     VMs (vCPU/RAM/schedule), Cloud Run Jobs, and Vertex AI.
package billing

import (
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kiri-dev/kiri/internal/httpx"
	"github.com/kiri-dev/kiri/internal/service"
	"github.com/kiri-dev/kiri/internal/storage"
)

const serviceName = "billing"

func init() {
	svc := New()
	_ = storage.Load(serviceName, "state", &svc.state)
	svc.ensureMaps()
	svc.seedDefaults()
	service.Register(svc)
}

// state is the full persisted service state.
type state struct {
	Accounts    map[string]*account `json:"accounts"`
	BillingInfo map[string]string   `json:"billingInfo"` // project -> billingAccountName
	Budgets     map[string]*budget  `json:"budgets"`     // full budget name -> budget
	Catalog     []*catalogService   `json:"catalog"`
	Costs       []*costLineItem     `json:"costs"`
}

type account struct {
	Name        string `json:"name"` // billingAccounts/{id}
	DisplayName string `json:"displayName"`
	Open        bool   `json:"open"`
}

type budget struct {
	Name           string           `json:"name"` // billingAccounts/{a}/budgets/{b}
	DisplayName    string           `json:"displayName"`
	Amount         map[string]any   `json:"amount,omitempty"`
	ThresholdRules []map[string]any `json:"thresholdRules,omitempty"`
	Etag           string           `json:"etag"`
}

type catalogService struct {
	ServiceID   string `json:"serviceId"`
	DisplayName string `json:"displayName"`
	SKUs        []*sku `json:"skus"`
}

type sku struct {
	SkuID       string  `json:"skuId"`
	Description string  `json:"description"`
	UnitPrice   float64 `json:"unitPrice"` // USD per unit
	Unit        string  `json:"unit"`
}

// costLineItem models one row of the detailed billing export.
type costLineItem struct {
	Service    string  `json:"service"`
	SKU        string  `json:"sku"`
	Project    string  `json:"project"`
	Label      string  `json:"label,omitempty"`
	Cost       float64 `json:"cost"`
	Currency   string  `json:"currency"`
	UsageStart string  `json:"usageStart"` // YYYY-MM-DD
	UsageEnd   string  `json:"usageEnd"`
}

// Service implements the billing/cost emulation.
type Service struct {
	mu    sync.RWMutex
	state state
}

// New creates an empty billing service.
func New() *Service {
	return &Service{state: state{
		Accounts:    map[string]*account{},
		BillingInfo: map[string]string{},
		Budgets:     map[string]*budget{},
	}}
}

func (s *Service) ensureMaps() {
	if s.state.Accounts == nil {
		s.state.Accounts = map[string]*account{}
	}

	if s.state.BillingInfo == nil {
		s.state.BillingInfo = map[string]string{}
	}

	if s.state.Budgets == nil {
		s.state.Budgets = map[string]*budget{}
	}
}

// Name returns the service name.
func (s *Service) Name() string { return serviceName }

// Meta returns catalog metadata.
func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "Cloud Billing",
		Category:    "Management & Billing",
		Description: "Billing accounts, budgets, pricing catalog, price calculator, and cost query",
		Fidelity:    service.FidelityA,
		State:       service.StateBehavioral,
	}
}

// seedDefaults installs default billing accounts, an official GCP pricing catalog,
// and realistic cost line items matching the GCP Pricing Calculator rules.
func (s *Service) seedDefaults() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.state.Accounts) == 0 {
		s.state.Accounts["billingAccounts/000000-000000-000000"] = &account{
			Name: "billingAccounts/000000-000000-000000", DisplayName: "kiri Billing Account (Gold Standard)", Open: true,
		}
	}

	if len(s.state.Catalog) == 0 {
		s.state.Catalog = []*catalogService{
			{
				ServiceID:   "6F81-5844-456A",
				DisplayName: "Compute Engine",
				SKUs: []*sku{
					{SkuID: "2E27-4F0B-8D3E", Description: "N1 Predefined vCPU running in Americas", UnitPrice: 0.031611, Unit: "h"},
					{SkuID: "8004-56A0-9F0B", Description: "N1 Predefined RAM running in Americas", UnitPrice: 0.004237, Unit: "GiB.h"},
					{SkuID: "E200-11AA-45BC", Description: "E2 Predefined vCPU running in Americas", UnitPrice: 0.022800, Unit: "h"},
					{SkuID: "E200-22BB-67DD", Description: "E2 Predefined RAM running in Americas", UnitPrice: 0.003060, Unit: "GiB.h"},
					{SkuID: "D90F-47A1-19B2", Description: "Storage PD Capacity", UnitPrice: 0.040000, Unit: "GiB.mo"},
					{SkuID: "F23A-8811-99CC", Description: "SSD backed PD Capacity", UnitPrice: 0.170000, Unit: "GiB.mo"},
				},
			},
			{
				ServiceID:   "95FF-2EF5-5EA1",
				DisplayName: "Cloud Storage",
				SKUs: []*sku{
					{SkuID: "E5F0-6A5D-7BAD", Description: "Standard Storage US", UnitPrice: 0.020000, Unit: "GiB.mo"},
					{SkuID: "D812-7F3A-010A", Description: "Nearline Storage US", UnitPrice: 0.010000, Unit: "GiB.mo"},
					{SkuID: "B901-44C2-81F0", Description: "Coldline Storage US", UnitPrice: 0.004000, Unit: "GiB.mo"},
					{SkuID: "A112-99B1-002C", Description: "Archive Storage US", UnitPrice: 0.001200, Unit: "GiB.mo"},
					{SkuID: "C120-77A1-33B2", Description: "Class A Operations", UnitPrice: 0.005000, Unit: "1k-ops"},
					{SkuID: "C120-77A2-33B3", Description: "Class B Operations", UnitPrice: 0.000400, Unit: "1k-ops"},
				},
			},
			{
				ServiceID:   "24E6-581D-38E5",
				DisplayName: "BigQuery",
				SKUs: []*sku{
					{SkuID: "24E6-581D-38E5-Q", Description: "Analysis (on-demand queries)", UnitPrice: 5.000000, Unit: "TiB"},
					{SkuID: "24E6-581D-38E5-S", Description: "Active Storage", UnitPrice: 0.020000, Unit: "GiB.mo"},
					{SkuID: "24E6-581D-38E5-L", Description: "Long-term Storage", UnitPrice: 0.010000, Unit: "GiB.mo"},
				},
			},
			{
				ServiceID:   "A916-0428-A68B",
				DisplayName: "Pub/Sub",
				SKUs: []*sku{
					{SkuID: "A916-0428-A68B-M", Description: "Message Ingestion & Delivery", UnitPrice: 0.040000, Unit: "GiB"},
				},
			},
			{
				ServiceID:   "23BC-0210-91CD",
				DisplayName: "Cloud Run & Cloud Run Jobs",
				SKUs: []*sku{
					{SkuID: "23BC-0210-CPU", Description: "CPU Allocation", UnitPrice: 0.000024, Unit: "vCPU.s"},
					{SkuID: "23BC-0210-RAM", Description: "Memory Allocation", UnitPrice: 0.0000025, Unit: "GiB.s"},
					{SkuID: "23BC-0210-REQ", Description: "Requests", UnitPrice: 0.400000, Unit: "1M-req"},
				},
			},
			{
				ServiceID:   "C902-881A-99FE",
				DisplayName: "Vertex AI",
				SKUs: []*sku{
					{SkuID: "C902-881A-TRAIN", Description: "Custom Training Node Hour", UnitPrice: 0.220000, Unit: "node.h"},
					{SkuID: "C902-881A-GPU", Description: "NVIDIA T4 GPU Hour", UnitPrice: 0.350000, Unit: "gpu.h"},
					{SkuID: "C902-881A-TIN", Description: "LLM Input Tokens", UnitPrice: 0.000150, Unit: "1k-tokens"},
					{SkuID: "C902-881A-TOUT", Description: "LLM Output Tokens", UnitPrice: 0.000600, Unit: "1k-tokens"},
				},
			},
		}
	}

	if len(s.state.Costs) == 0 {
		today := time.Now().UTC()
		start := today.AddDate(0, 0, -1).Format("2006-01-02")
		end := today.Format("2006-01-02")
		s.state.Costs = []*costLineItem{
			{Service: "Compute Engine", SKU: "N1 Predefined vCPU running in Americas", Project: "demo-project", Cost: 23.07, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "Compute Engine", SKU: "N1 Predefined RAM running in Americas", Project: "demo-project", Cost: 12.37, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "Cloud Storage", SKU: "Standard Storage US", Project: "demo-project", Cost: 4.50, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "BigQuery", SKU: "Analysis (on-demand queries)", Project: "demo-project", Cost: 15.00, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "Pub/Sub", SKU: "Message Ingestion & Delivery", Project: "demo-project", Cost: 2.40, Currency: "USD", UsageStart: start, UsageEnd: end},
		}
	}
}

// RegisterRoutes registers all billing/cost routes.
func (s *Service) RegisterRoutes(r service.Router) {
	// Cloud Billing v1.
	r.Handle("GET", "/v1/billingAccounts", s.listAccounts)
	r.Handle("POST", "/v1/billingAccounts", s.createAccount)
	r.Handle("GET", "/v1/billingAccounts/{account}", s.getAccount)
	r.Handle("GET", "/v1/projects/{project}/billingInfo", s.getBillingInfo)
	r.Handle("PUT", "/v1/projects/{project}/billingInfo", s.updateBillingInfo)
	r.Handle("GET", "/v1/services", s.listCatalogServices)
	r.Handle("GET", "/v1/services/{service}/skus", s.listSKUs)

	// Billing Budgets v1.
	r.Handle("GET", "/v1/billingAccounts/{account}/budgets", s.listBudgets)
	r.Handle("POST", "/v1/billingAccounts/{account}/budgets", s.createBudget)
	r.Handle("GET", "/v1/billingAccounts/{account}/budgets/{budget}", s.getBudget)
	r.Handle("PATCH", "/v1/billingAccounts/{account}/budgets/{budget}", s.patchBudget)
	r.Handle("DELETE", "/v1/billingAccounts/{account}/budgets/{budget}", s.deleteBudget)

	// kiri cost query and price calculator surfaces.
	r.Handle("POST", "/kiri/billing/cost", s.queryCost)
	r.Handle("POST", "/kiri/billing/seed", s.seedCost)
	r.Handle("POST", "/kiri/billing/calculator", s.calculateEstimate)
}

// Close persists state if configured.
func (s *Service) Close() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return storage.Save(serviceName, "state", s.state)
}

// ---- Cloud Billing ----

func (s *Service) listAccounts(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]*account, 0, len(s.state.Accounts))
	for _, a := range s.state.Accounts {
		items = append(items, a)
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"billingAccounts": items})
}

func (s *Service) createAccount(w http.ResponseWriter, r *http.Request) {
	var body account
	_ = httpx.DecodeJSON(r, &body)

	id := "billingAccounts/" + strings.ToUpper(httpx.ID(3)) + "-" + strings.ToUpper(httpx.ID(3)) + "-" + strings.ToUpper(httpx.ID(3))
	a := &account{Name: id, DisplayName: body.DisplayName, Open: true}

	s.mu.Lock()
	s.state.Accounts[id] = a
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) getAccount(w http.ResponseWriter, r *http.Request) {
	name := "billingAccounts/" + r.PathValue("account")

	s.mu.RLock()
	a, ok := s.state.Accounts[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "billing account not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, a)
}

func (s *Service) getBillingInfo(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	s.mu.RLock()
	billingAccount := s.state.BillingInfo[project]
	s.mu.RUnlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":               "projects/" + project + "/billingInfo",
		"projectId":          project,
		"billingAccountName": billingAccount,
		"billingEnabled":     billingAccount != "",
	})
}

func (s *Service) updateBillingInfo(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var body struct {
		BillingAccountName string `json:"billingAccountName"`
	}
	_ = httpx.DecodeJSON(r, &body)

	s.mu.Lock()
	s.state.BillingInfo[project] = body.BillingAccountName
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"name":               "projects/" + project + "/billingInfo",
		"projectId":          project,
		"billingAccountName": body.BillingAccountName,
		"billingEnabled":     body.BillingAccountName != "",
	})
}

func (s *Service) listCatalogServices(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]map[string]any, 0, len(s.state.Catalog))
	for _, c := range s.state.Catalog {
		items = append(items, map[string]any{
			"name":        "services/" + c.ServiceID,
			"serviceId":   c.ServiceID,
			"displayName": c.DisplayName,
		})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"services": items})
}

func (s *Service) listSKUs(w http.ResponseWriter, r *http.Request) {
	serviceID := r.PathValue("service")

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, c := range s.state.Catalog {
		if c.ServiceID != serviceID {
			continue
		}

		items := make([]map[string]any, 0, len(c.SKUs))
		for _, k := range c.SKUs {
			units := int64(k.UnitPrice)
			nanos := int64((k.UnitPrice - float64(units)) * 1e9)
			items = append(items, map[string]any{
				"name":        "services/" + serviceID + "/skus/" + k.SkuID,
				"skuId":       k.SkuID,
				"description": k.Description,
				"pricingInfo": []map[string]any{{
					"pricingExpression": map[string]any{
						"usageUnit": k.Unit,
						"tieredRates": []map[string]any{{
							"startUsageAmount": 0,
							"unitPrice":        map[string]any{"currencyCode": "USD", "units": units, "nanos": nanos},
						}},
					},
				}},
			})
		}

		httpx.WriteJSON(w, http.StatusOK, map[string]any{"skus": items})

		return
	}

	httpx.NotFound(w, "service not found: "+serviceID)
}

// ---- Billing Budgets ----

func (s *Service) listBudgets(w http.ResponseWriter, r *http.Request) {
	prefix := "billingAccounts/" + r.PathValue("account") + "/budgets/"

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]*budget, 0)

	for name, b := range s.state.Budgets {
		if strings.HasPrefix(name, prefix) {
			items = append(items, b)
		}
	}

	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"budgets": items})
}

func (s *Service) createBudget(w http.ResponseWriter, r *http.Request) {
	acct := r.PathValue("account")

	var body budget
	if err := httpx.DecodeJSON(r, &body); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	body.Name = "billingAccounts/" + acct + "/budgets/" + httpx.ID(8)
	body.Etag = httpx.ID(8)

	s.mu.Lock()
	s.state.Budgets[body.Name] = &body
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, body)
}

func (s *Service) getBudget(w http.ResponseWriter, r *http.Request) {
	name := "billingAccounts/" + r.PathValue("account") + "/budgets/" + r.PathValue("budget")

	s.mu.RLock()
	b, ok := s.state.Budgets[name]
	s.mu.RUnlock()

	if !ok {
		httpx.NotFound(w, "budget not found: "+name)

		return
	}

	httpx.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) patchBudget(w http.ResponseWriter, r *http.Request) {
	name := "billingAccounts/" + r.PathValue("account") + "/budgets/" + r.PathValue("budget")

	s.mu.Lock()
	defer s.mu.Unlock()

	b, ok := s.state.Budgets[name]
	if !ok {
		httpx.NotFound(w, "budget not found: "+name)

		return
	}

	var patch budget
	_ = httpx.DecodeJSON(r, &patch)

	if patch.DisplayName != "" {
		b.DisplayName = patch.DisplayName
	}

	if patch.Amount != nil {
		b.Amount = patch.Amount
	}

	if patch.ThresholdRules != nil {
		b.ThresholdRules = patch.ThresholdRules
	}

	b.Etag = httpx.ID(8)

	httpx.WriteJSON(w, http.StatusOK, b)
}

func (s *Service) deleteBudget(w http.ResponseWriter, r *http.Request) {
	name := "billingAccounts/" + r.PathValue("account") + "/budgets/" + r.PathValue("budget")

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.state.Budgets[name]; !ok {
		httpx.NotFound(w, "budget not found: "+name)

		return
	}

	delete(s.state.Budgets, name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

// ---- Cost query (Cost Explorer analogue) ----

type costQueryRequest struct {
	Start   string `json:"start"`   // YYYY-MM-DD inclusive
	End     string `json:"end"`     // YYYY-MM-DD exclusive
	GroupBy string `json:"groupBy"` // "service" | "sku" | "project" | "label"
	Filter  struct {
		Service string `json:"service"`
		Project string `json:"project"`
	} `json:"filter"`
}

func (s *Service) queryCost(w http.ResponseWriter, r *http.Request) {
	var req costQueryRequest
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	if req.GroupBy == "" {
		req.GroupBy = "service"
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	groups := map[string]float64{}
	currency := "USD"

	var total float64

	for _, c := range s.state.Costs {
		if req.Start != "" && c.UsageEnd < req.Start {
			continue
		}

		if req.End != "" && c.UsageStart >= req.End {
			continue
		}

		if req.Filter.Service != "" && c.Service != req.Filter.Service {
			continue
		}

		if req.Filter.Project != "" && c.Project != req.Filter.Project {
			continue
		}

		var key string

		switch req.GroupBy {
		case "sku":
			key = c.SKU
		case "project":
			key = c.Project
		case "label":
			key = c.Label
		default:
			key = c.Service
		}

		groups[key] += c.Cost
		total += c.Cost

		if c.Currency != "" {
			currency = c.Currency
		}
	}

	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	results := make([]map[string]any, 0, len(keys))
	for _, k := range keys {
		results = append(results, map[string]any{"key": k, "cost": round2(groups[k]), "currency": currency})
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"groupBy":  req.GroupBy,
		"currency": currency,
		"total":    round2(total),
		"groups":   results,
	})
}

func (s *Service) seedCost(w http.ResponseWriter, r *http.Request) {
	var items []*costLineItem
	if err := httpx.DecodeJSON(r, &items); err != nil {
		httpx.BadRequest(w, err.Error())

		return
	}

	s.mu.Lock()

	for _, it := range items {
		if it.Currency == "" {
			it.Currency = "USD"
		}

		s.state.Costs = append(s.state.Costs, it)
	}

	n := len(s.state.Costs)
	s.mu.Unlock()

	httpx.WriteJSON(w, http.StatusOK, map[string]any{"added": len(items), "total": n})
}

// ---- GCP Price Calculator Surface ----

type calculatorItem struct {
	Service              string  `json:"service"`              // "compute" | "gcs" | "bigquery" | "pubsub" | "cloudrun" | "cloudrunjobs" | "vertexai"
	MachineType          string  `json:"machineType"`          // e.g. "n1-standard-1", "e2-standard-4"
	Instances            int     `json:"instances"`            // number of instances (default 1)
	VCPUs                float64 `json:"vcpus"`                // total vCPUs if custom spec (e.g. 4)
	MemoryGiB            float64 `json:"memoryGiB"`            // total memory in GiB (e.g. 7.0)
	StorageGiB           float64 `json:"storageGiB"`           // persistent storage in GiB
	StorageClass         string  `json:"storageClass"`         // "standard" | "nearline" | "coldline" | "archive"
	HoursPerMonth        float64 `json:"hoursPerMonth"`        // total runtime hours per month (default 730 if unset)
	DailyOnHours         float64 `json:"dailyOnHours"`         // hours ON per day (e.g. 9.0 -> 270h/mo)
	QueriesTiB           float64 `json:"queriesTiB"`           // BigQuery query volume in TiB
	IngestionGiB         float64 `json:"ingestionGiB"`         // Pub/Sub or log volume in GiB
	RequestsCount        float64 `json:"requestsCount"`        // Invocations or HTTP requests
	ExecutionSeconds     float64 `json:"executionSeconds"`     // Cloud Run Jobs total task execution duration in seconds
	TaskCount            int     `json:"taskCount"`            // Cloud Run Jobs number of task runs
	TaskDurationSeconds  float64 `json:"taskDurationSeconds"`  // Duration per task run in seconds
	NodeHours            float64 `json:"nodeHours"`            // Vertex AI training node hours
	GPUCount             int     `json:"gpuCount"`             // Vertex AI GPUs
	InputTokens          float64 `json:"inputTokens"`          // Vertex AI LLM input tokens (in thousands)
	OutputTokens         float64 `json:"outputTokens"`         // Vertex AI LLM output tokens (in thousands)
}

type calculatorLineItem struct {
	SKUDescription string  `json:"skuDescription"`
	Formula        string  `json:"formula"`
	UnitPrice      float64 `json:"unitPrice"`
	UsageAmount    float64 `json:"usageAmount"`
	Unit           string  `json:"unit"`
	MonthlyCost    float64 `json:"monthlyCost"`
}

func (s *Service) calculateEstimate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Items []calculatorItem `json:"items"`
	}
	if err := httpx.DecodeJSON(r, &req); err != nil {
		httpx.BadRequest(w, err.Error())
		return
	}

	if len(req.Items) == 0 {
		httpx.BadRequest(w, "items array cannot be empty")
		return
	}

	var totalCost float64
	var lineItems []calculatorLineItem

	for _, item := range req.Items {
		hours := item.HoursPerMonth
		if hours <= 0 {
			if item.DailyOnHours > 0 {
				hours = item.DailyOnHours * 30 // e.g. 9h ON per day * 30 days = 270h/mo
			} else {
				hours = 730 // Standard GCP billing month hours (365 * 24 / 12)
			}
		}

		instances := item.Instances
		if instances <= 0 {
			instances = 1
		}

		svcLower := strings.ToLower(item.Service)

		switch svcLower {
		case "compute", "compute engine", "gce", "vm", "vms":
			vcpus := item.VCPUs
			mem := item.MemoryGiB

			if item.MachineType != "" {
				switch strings.ToLower(item.MachineType) {
				case "n1-standard-1":
					vcpus, mem = 1, 3.75
				case "n1-standard-2":
					vcpus, mem = 2, 7.5
				case "n1-standard-4":
					vcpus, mem = 4, 15.0
				case "e2-standard-2":
					vcpus, mem = 2, 8.0
				case "e2-standard-4":
					vcpus, mem = 4, 16.0
				case "e2-micro":
					vcpus, mem = 0.25, 1.0
				default:
					if vcpus <= 0 {
						vcpus = 1
					}
					if mem <= 0 {
						mem = 3.75
					}
				}
			} else {
				if vcpus <= 0 {
					vcpus = 1
				}
				if mem <= 0 {
					mem = 3.75
				}
			}

			// vCPU cost: $0.031611 per vCPU-hour
			vcpuHours := vcpus * float64(instances) * hours
			vcpuCost := vcpuHours * 0.031611
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "N1 Predefined vCPU running in Americas",
				Formula:        fmt.Sprintf("%.2f vCPUs * %d instances * %.1f hrs/mo * $0.031611", vcpus, instances, hours),
				UnitPrice:      0.031611,
				UsageAmount:    round2(vcpuHours),
				Unit:           "h",
				MonthlyCost:    round2(vcpuCost),
			})

			// Memory cost: $0.004237 per GiB-hour
			ramHours := mem * float64(instances) * hours
			ramCost := ramHours * 0.004237
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "N1 Predefined RAM running in Americas",
				Formula:        fmt.Sprintf("%.2f GiB RAM * %d instances * %.1f hrs/mo * $0.004237", mem, instances, hours),
				UnitPrice:      0.004237,
				UsageAmount:    round2(ramHours),
				Unit:           "GiB.h",
				MonthlyCost:    round2(ramCost),
			})

			// Persistent Disk Capacity: Disk persists even when VM is turned OFF (billed 730h/month)
			if item.StorageGiB > 0 {
				diskCost := item.StorageGiB * float64(instances) * 0.040
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "Storage PD Capacity (Persistent)",
					Formula:        fmt.Sprintf("%.1f GiB * %d instances * $0.040/GiB-mo", item.StorageGiB, instances),
					UnitPrice:      0.040000,
					UsageAmount:    round2(item.StorageGiB * float64(instances)),
					Unit:           "GiB.mo",
					MonthlyCost:    round2(diskCost),
				})
				totalCost += diskCost
			}

			totalCost += vcpuCost + ramCost

		case "cloudrunjobs", "cloud run jobs", "run jobs", "cloudrun job":
			totalExecSec := item.ExecutionSeconds
			if totalExecSec <= 0 && item.TaskCount > 0 && item.TaskDurationSeconds > 0 {
				totalExecSec = float64(item.TaskCount) * item.TaskDurationSeconds
			}
			if totalExecSec <= 0 {
				totalExecSec = 3600 // Default 1 hour of execution
			}

			vcpus := item.VCPUs
			if vcpus <= 0 {
				vcpus = 1
			}
			mem := item.MemoryGiB
			if mem <= 0 {
				mem = 2.0
			}

			vcpuSec := vcpus * float64(instances) * totalExecSec
			vcpuCost := vcpuSec * 0.000024
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "Cloud Run Jobs CPU Allocation",
				Formula:        fmt.Sprintf("%.2f vCPUs * %d instances * %.1f sec * $0.000024/vCPU-s", vcpus, instances, totalExecSec),
				UnitPrice:      0.000024,
				UsageAmount:    round2(vcpuSec),
				Unit:           "vCPU.s",
				MonthlyCost:    round2(vcpuCost),
			})

			memSec := mem * float64(instances) * totalExecSec
			memCost := memSec * 0.0000025
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "Cloud Run Jobs Memory Allocation",
				Formula:        fmt.Sprintf("%.2f GiB * %d instances * %.1f sec * $0.0000025/GiB-s", mem, instances, totalExecSec),
				UnitPrice:      0.0000025,
				UsageAmount:    round2(memSec),
				Unit:           "GiB.s",
				MonthlyCost:    round2(memCost),
			})

			totalCost += vcpuCost + memCost

		case "vertexai", "vertex ai", "vertex":
			nodeHours := item.NodeHours
			if nodeHours <= 0 {
				if item.DailyOnHours > 0 {
					nodeHours = item.DailyOnHours * 30
				} else {
					nodeHours = hours
				}
			}

			if item.NodeHours > 0 || item.VCPUs > 0 || item.GPUCount > 0 {
				trainingCost := nodeHours * float64(instances) * 0.220000
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "Vertex AI Custom Training Node Hour",
					Formula:        fmt.Sprintf("%d nodes * %.1f hrs * $0.22/node-h", instances, nodeHours),
					UnitPrice:      0.220000,
					UsageAmount:    round2(nodeHours * float64(instances)),
					Unit:           "node.h",
					MonthlyCost:    round2(trainingCost),
				})
				totalCost += trainingCost

				if item.GPUCount > 0 {
					gpuHours := float64(item.GPUCount*instances) * nodeHours
					gpuCost := gpuHours * 0.350000
					lineItems = append(lineItems, calculatorLineItem{
						SKUDescription: "NVIDIA T4 GPU Hour",
						Formula:        fmt.Sprintf("%d GPUs * %.1f hrs * $0.35/gpu-h", item.GPUCount*instances, nodeHours),
						UnitPrice:      0.350000,
						UsageAmount:    round2(gpuHours),
						Unit:           "gpu.h",
						MonthlyCost:    round2(gpuCost),
					})
					totalCost += gpuCost
				}
			}

			if item.InputTokens > 0 {
				inTokenCost := (item.InputTokens / 1000.0) * 0.00015
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "LLM Input Tokens",
					Formula:        fmt.Sprintf("%.1f k-tokens * $0.00015/1k-tokens", item.InputTokens),
					UnitPrice:      0.000150,
					UsageAmount:    round2(item.InputTokens),
					Unit:           "1k-tokens",
					MonthlyCost:    round2(inTokenCost),
				})
				totalCost += inTokenCost
			}

			if item.OutputTokens > 0 {
				outTokenCost := (item.OutputTokens / 1000.0) * 0.0006
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "LLM Output Tokens",
					Formula:        fmt.Sprintf("%.1f k-tokens * $0.0006/1k-tokens", item.OutputTokens),
					UnitPrice:      0.000600,
					UsageAmount:    round2(item.OutputTokens),
					Unit:           "1k-tokens",
					MonthlyCost:    round2(outTokenCost),
				})
				totalCost += outTokenCost
			}

		case "gcs", "cloud storage", "storage":
			rate := 0.020 // Standard US
			skuDesc := "Standard Storage US"

			switch strings.ToLower(item.StorageClass) {
			case "nearline":
				rate = 0.010
				skuDesc = "Nearline Storage US"
			case "coldline":
				rate = 0.004
				skuDesc = "Coldline Storage US"
			case "archive":
				rate = 0.0012
				skuDesc = "Archive Storage US"
			}

			storageCost := item.StorageGiB * rate
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: skuDesc,
				Formula:        fmt.Sprintf("%.1f GiB * $%.4f/GiB-mo", item.StorageGiB, rate),
				UnitPrice:      rate,
				UsageAmount:    round2(item.StorageGiB),
				Unit:           "GiB.mo",
				MonthlyCost:    round2(storageCost),
			})
			totalCost += storageCost

		case "bigquery", "bq":
			if item.QueriesTiB > 0 {
				queryCost := item.QueriesTiB * 5.00
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "Analysis (on-demand queries)",
					Formula:        fmt.Sprintf("%.2f TiB * $5.00/TiB", item.QueriesTiB),
					UnitPrice:      5.000000,
					UsageAmount:    round2(item.QueriesTiB),
					Unit:           "TiB",
					MonthlyCost:    round2(queryCost),
				})
				totalCost += queryCost
			}

			if item.StorageGiB > 0 {
				bqStorageCost := item.StorageGiB * 0.020
				lineItems = append(lineItems, calculatorLineItem{
					SKUDescription: "Active Storage",
					Formula:        fmt.Sprintf("%.1f GiB * $0.020/GiB-mo", item.StorageGiB),
					UnitPrice:      0.020000,
					UsageAmount:    round2(item.StorageGiB),
					Unit:           "GiB.mo",
					MonthlyCost:    round2(bqStorageCost),
				})
				totalCost += bqStorageCost
			}

		case "pubsub", "pub/sub":
			ingestCost := item.IngestionGiB * 0.040
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "Message Ingestion & Delivery",
				Formula:        fmt.Sprintf("%.1f GiB * $0.040/GiB", item.IngestionGiB),
				UnitPrice:      0.040000,
				UsageAmount:    round2(item.IngestionGiB),
				Unit:           "GiB",
				MonthlyCost:    round2(ingestCost),
			})
			totalCost += ingestCost

		case "cloudrun", "cloud run":
			reqMillions := item.RequestsCount / 1000000.0
			reqCost := reqMillions * 0.40
			lineItems = append(lineItems, calculatorLineItem{
				SKUDescription: "Requests",
				Formula:        fmt.Sprintf("%.2f M requests * $0.40/M-req", reqMillions),
				UnitPrice:      0.400000,
				UsageAmount:    round2(reqMillions),
				Unit:           "1M-req",
				MonthlyCost:    round2(reqCost),
			})
			totalCost += reqCost
		}
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"currency":     "USD",
		"monthlyTotal": round2(totalCost),
		"lineItems":    lineItems,
		"disclaimer":   "Prices estimated using official Google Cloud Pricing Calculator rules (Americas multi-region rates).",
	})
}

func round2(f float64) float64 {
	return math.Round(f*100) / 100
}
