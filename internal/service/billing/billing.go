// Package billing emulates GCP's cost and billing surface — the closest
// analogue to AWS Cost Explorer. It covers four cooperating surfaces:
//
//  1. Cloud Billing (cloudbilling.googleapis.com/v1): billing accounts, project
//     billing info, and the pricing catalog (services + SKUs).
//  2. Billing Budgets (billingbudgets.googleapis.com/v1): budgets CRUD.
//  3. Cost query (/kiri/billing/cost): line items grouped by service/sku/project
//     over a time window — the GetCostAndUsage analogue.
//  4. Detailed export seeding (/kiri/billing/seed): inject cost line items.
package billing

import (
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
		Description: "Billing accounts, budgets, pricing catalog, and cost query (Cost Explorer analogue)",
		Fidelity:    service.FidelityA,
		State:       service.StateBehavioral,
	}
}

// seedDefaults installs a default billing account, a small pricing catalog and
// a handful of cost line items so the cost query returns data out of the box.
func (s *Service) seedDefaults() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.state.Accounts) == 0 {
		s.state.Accounts["billingAccounts/000000-000000-000000"] = &account{
			Name: "billingAccounts/000000-000000-000000", DisplayName: "kiri Billing Account", Open: true,
		}
	}

	if len(s.state.Catalog) == 0 {
		s.state.Catalog = []*catalogService{
			{ServiceID: "6F81-5844-456A", DisplayName: "Compute Engine", SKUs: []*sku{
				{SkuID: "2E27-4F0B-8D3E", Description: "N1 Predefined vCPU running", UnitPrice: 0.031611, Unit: "h"},
				{SkuID: "8004-56A0-9F0B", Description: "N1 Predefined RAM running", UnitPrice: 0.004237, Unit: "GiB.h"},
			}},
			{ServiceID: "95FF-2EF5-5EA1", DisplayName: "Cloud Storage", SKUs: []*sku{
				{SkuID: "E5F0-6A5D-7BAD", Description: "Standard Storage US", UnitPrice: 0.020, Unit: "GiB.mo"},
			}},
			{ServiceID: "24E6-581D-38E5", DisplayName: "BigQuery", SKUs: []*sku{
				{SkuID: "24E6-581D-38E5-Q", Description: "Analysis (on-demand queries)", UnitPrice: 5.0, Unit: "TiB"},
			}},
		}
	}

	if len(s.state.Costs) == 0 {
		today := time.Now().UTC()
		start := today.AddDate(0, 0, -1).Format("2006-01-02")
		end := today.Format("2006-01-02")
		s.state.Costs = []*costLineItem{
			{Service: "Compute Engine", SKU: "N1 Predefined vCPU running", Project: "demo-project", Cost: 12.34, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "Cloud Storage", SKU: "Standard Storage US", Project: "demo-project", Cost: 3.21, Currency: "USD", UsageStart: start, UsageEnd: end},
			{Service: "BigQuery", SKU: "Analysis (on-demand queries)", Project: "demo-project", Cost: 7.89, Currency: "USD", UsageStart: start, UsageEnd: end},
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

	// kiri cost query surface (Cost Explorer analogue).
	r.Handle("POST", "/kiri/billing/cost", s.queryCost)
	r.Handle("POST", "/kiri/billing/seed", s.seedCost)
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
							"unitPrice": map[string]any{"currencyCode": "USD", "units": units, "nanos": nanos},
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

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}
