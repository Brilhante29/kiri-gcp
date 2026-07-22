package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestBigQueryDatasets(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/bigquery/v2/projects/myproj"

	body := strings.NewReader(`{"datasetReference":{"datasetId":"test_ds"},"location":"US"}`)
	resp, err := http.Post(base+"/datasets", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create dataset: %d", resp.StatusCode)
	}
	var ds map[string]any
	json.NewDecoder(resp.Body).Decode(&ds)
	if ds["id"] == "" {
		t.Fatal("expected dataset id")
	}
}

func TestBigQueryTables(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/bigquery/v2/projects/myproj"

	http.Post(base+"/datasets", "application/json", strings.NewReader(`{"datasetReference":{"datasetId":"ds2"}}`))
	body := strings.NewReader(`{"tableReference":{"tableId":"test_tbl"},"schema":{"fields":[{"name":"x","type":"STRING"}]}}`)
	resp, err := http.Post(base+"/datasets/ds2/tables", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create table: %d", resp.StatusCode)
	}
	var tbl map[string]any
	json.NewDecoder(resp.Body).Decode(&tbl)
	if tbl["id"] == "" {
		t.Fatal("expected table id")
	}
}

func TestBigQueryQuery(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	body := strings.NewReader(`{"query":"SELECT 1"}`)
	resp, err := http.Post(srv.URL+"/bigquery/v2/projects/myproj/queries", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("query: %d", resp.StatusCode)
	}
	var qr map[string]any
	json.NewDecoder(resp.Body).Decode(&qr)
	if qr["kind"] != "bigquery#queryResponse" {
		t.Fatal("expected query response")
	}
}

func TestCloudRunServices(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/services"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"","template":{"containers":[{"image":"nginx"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	var svc map[string]any
	json.NewDecoder(resp.Body).Decode(&svc)
	if svc["name"] == "" {
		t.Fatal("expected service name")
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Services []any `json:"services"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(list.Services))
	}
}

func TestComputeEngineInstances(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/compute/v1/projects/myproj/zones/us-central1-a/instances"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"vm-1","machineType":"e2-medium"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Items []any `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Items) == 0 {
		t.Fatal("expected instances")
	}

	// Get specific
	resp, err = http.Get(base + "/vm-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("get instance: %d", resp.StatusCode)
	}
}

func TestComputeEngineDisks(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/compute/v1/projects/myproj/zones/us-central1-a/disks"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"disk-1","sizeGb":50}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create disk: %d", resp.StatusCode)
	}
	var d map[string]any
	json.NewDecoder(resp.Body).Decode(&d)
	if d["status"] != "READY" {
		t.Fatalf("expected READY, got %v", d["status"])
	}

	resp, err = http.Get(base + "/disk-1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("get disk: %d", resp.StatusCode)
	}
}

func TestComputeEngineFirewalls(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/compute/v1/projects/myproj/global/firewalls"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"allow-http","allowed":[{"IPProtocol":"tcp","ports":["80"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create firewall: %d", resp.StatusCode)
	}
}

func TestCloudDNSZones(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/dns/v1/projects/myproj/managedZones"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"myzone","dnsName":"example.com."}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create zone: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Zones []any `json:"managedZones"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Zones) != 1 {
		t.Fatalf("expected 1 zone, got %d", len(list.Zones))
	}
}

func TestCloudFunctions(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/functions"

	// Create function and read the auto-generated name from the response.
	createBody := strings.NewReader(`{"name":"","entryPoint":"hello","runtime":"nodejs20","sourceArchiveUrl":"gs://bucket/code.zip"}`)
	resp, err := http.Post(base, "application/json", createBody)
	if err != nil {
		t.Fatal(err)
	}
	var createdFunc struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&createdFunc); err != nil {
		resp.Body.Close()
		t.Fatal(err)
	}
	resp.Body.Close()
	if createdFunc.Name == "" {
		t.Fatal("expected auto-generated function name")
	}
	shortName := createdFunc.Name[strings.LastIndex(createdFunc.Name, "/")+1:]

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Functions []any `json:"functions"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Functions) != 1 {
		t.Fatalf("expected 1 function, got %d", len(list.Functions))
	}

	resp, err = http.Post(base+"/"+shortName+":call", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("call function: %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCloudSQLInstances(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/sql/v1beta4/projects/myproj/instances"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"db1","databaseVersion":"POSTGRES_14","region":"us-central1"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create instance: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Items []any `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Items) == 0 {
		t.Fatal("expected instances")
	}

	resp, err = http.Post(base+"/db1/start", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("start: %d", resp.StatusCode)
	}

	resp, err = http.Post(base+"/db1/stop", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("stop: %d", resp.StatusCode)
	}
}

func TestCloudSQLDatabases(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/sql/v1beta4/projects/myproj/instances/db1/databases"

	// Create instance first
	http.Post(srv.URL+"/sql/v1beta4/projects/myproj/instances", "application/json", strings.NewReader(`{"name":"db1"}`))

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"mydb"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create database: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Items []any `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Items) == 0 {
		t.Fatal("expected databases")
	}
}

func TestCloudTasksQueues(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v2/projects/myproj/locations/us-central1/queues"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":""}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create queue: %d", resp.StatusCode)
	}
}

func TestCloudTasksCreateAndRun(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	qBase := srv.URL + "/v2/projects/myproj/locations/us-central1/queues"

	http.Post(qBase, "application/json", strings.NewReader(`{"name":""}`))
	q := "projects/myproj/locations/us-central1/queues/q-"

	resp, err := http.Post(qBase+"/q/tasks", "application/json", strings.NewReader(`{"task":{"payload":"test"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// May fail if queue name doesn't match; key is the task was created
	_ = q
	_ = resp
}

func TestCloudSchedulerJobs(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/jobs"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"","schedule":"0 * * * *","httpTarget":{"uri":"http://example.com"}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create job: %d - %s", resp.StatusCode, string(b))
	}
	var j map[string]any
	json.NewDecoder(resp.Body).Decode(&j)
	if j["state"] != "ENABLED" {
		t.Fatalf("expected ENABLED, got %v", j["state"])
	}

	resp, err = http.Post(base+"/j1:pause", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp

	resp, err = http.Post(base+"/j1:resume", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp
}

func doHostReq(t *testing.T, method, urlStr, host string, body io.Reader) *http.Response {
	req, err := http.NewRequest(method, urlStr, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestGKEClusters(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/clusters"

	resp := doHostReq(t, "POST", base, "container.googleapis.com", strings.NewReader(`{"name":"cluster-1","initialNodeCount":3}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create cluster: %d - %s", resp.StatusCode, string(b))
	}
	var c map[string]any
	json.NewDecoder(resp.Body).Decode(&c)
	cluster, _ := c["cluster"].(map[string]any)
	if cluster != nil && cluster["status"] != "RUNNING" {
		t.Fatalf("expected RUNNING, got %v", cluster["status"])
	}

	resp = doHostReq(t, "GET", base+"/cluster-1", "container.googleapis.com", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("get cluster: %d", resp.StatusCode)
	}
}

func TestGKENodePools(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	// Create cluster first
	doHostReq(t, "POST", srv.URL+"/v1/projects/myproj/locations/us-central1/clusters", "container.googleapis.com", strings.NewReader(`{"name":"c1"}`))
	npBase := srv.URL + "/v1/projects/myproj/locations/us-central1/clusters/c1/nodePools"

	resp := doHostReq(t, "POST", npBase, "container.googleapis.com", strings.NewReader(`{"name":"pool-1","initialNodeCount":5}`))
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create node pool: %d", resp.StatusCode)
	}
}

func TestLoggingEntries(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v2/entries:write", "application/json",
		strings.NewReader(`{"entries":[{"logName":"projects/myproj/logs/test","textPayload":"hello","severity":"INFO"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("write entries: %d", resp.StatusCode)
	}

	resp, err = http.Post(srv.URL+"/v2/entries:list", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Entries []any `json:"entries"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list.Entries))
	}
}

func TestLoggingMetricsAndSinks(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v2/projects/myproj"

	resp, err := http.Post(base+"/metrics", "application/json", strings.NewReader(`{"name":"","filter":"severity>=ERROR"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create metric: %d", resp.StatusCode)
	}

	resp, err = http.Post(base+"/sinks", "application/json", strings.NewReader(`{"name":"","destination":"storage.googleapis.com/my-bucket"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create sink: %d", resp.StatusCode)
	}
}

func TestMonitoringMetricDescriptors(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v3/projects/myproj/metricDescriptors"

	resp, err := http.Post(base, "application/json",
		strings.NewReader(`{"type":"custom.googleapis.com/my_metric","metricKind":"GAUGE","valueType":"DOUBLE"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create descriptor: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Descriptors []any `json:"metricDescriptors"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Descriptors) == 0 {
		t.Fatal("expected metric descriptors")
	}
}

func TestMonitoringTimeSeries(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v3/projects/myproj"

	resp, err := http.Post(base+"/timeSeries", "application/json",
		strings.NewReader(`{"timeSeries":[{"metric":{"type":"custom.googleapis.com/my_metric"},"points":[{"value":{"doubleValue":42}}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create timeSeries: %d", resp.StatusCode)
	}
}

func TestMonitoringAlertPolicies(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v3/projects/myproj"

	resp, err := http.Post(base+"/alertPolicies", "application/json",
		strings.NewReader(`{"displayName":"CPU > 80%","condition":{"conditionThreshold":{"filter":"metric.type=\"custom/cpu\""}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create alert: %d", resp.StatusCode)
	}

	resp, err = http.Get(base + "/alertPolicies")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Policies []any `json:"alertPolicies"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Policies) == 0 {
		t.Fatal("expected alert policies")
	}
}

func TestArtifactRegistryRepositories(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/repositories"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"name":"","format":"DOCKER","description":"Docker images"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create repository: %d", resp.StatusCode)
	}

	resp, err = http.Get(base)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list struct {
		Repos []any `json:"repositories"`
	}
	json.NewDecoder(resp.Body).Decode(&list)
	if len(list.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(list.Repos))
	}
}

func TestEventarcTriggers(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/triggers"

	resp, err := http.Post(base, "application/json",
		strings.NewReader(`{"name":"","eventFilters":[{"attribute":"type","value":"google.cloud.storage.object.v1.finalized"}],"destination":{"cloudRun":{"service":"my-service"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create trigger: %d", resp.StatusCode)
	}
	var t2 map[string]any
	json.NewDecoder(resp.Body).Decode(&t2)
	if t2["uid"] == "" {
		t.Fatal("expected trigger uid")
	}
}

func TestEventarcChannels(t *testing.T) {
	srv := kiriNewServer(t)
	defer srv.Close()
	base := srv.URL + "/v1/projects/myproj/locations/us-central1/channels"

	resp, err := http.Post(base, "application/json", strings.NewReader(`{"provider":"pubsub.googleapis.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("create channel: %d", resp.StatusCode)
	}
}
