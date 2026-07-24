package bigquery

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/Brilhante29/kiri-gcp/internal/httpx"
	"github.com/Brilhante29/kiri-gcp/internal/service"
	"github.com/Brilhante29/kiri-gcp/internal/storage"
)

const serviceName = "bigquery"

func init() { service.Register(New()) }

type dataset struct {
	Kind             string            `json:"kind"`
	ID               string            `json:"id"`
	DatasetReference map[string]string `json:"datasetReference"`
	Location         string            `json:"location"`
	CreationTime     string            `json:"creationTime"`
}

type table struct {
	Kind           string            `json:"kind"`
	ID             string            `json:"id"`
	TableReference map[string]string `json:"tableReference"`
	CreationTime   string            `json:"creationTime"`
	NumRows        string            `json:"numRows"`
	NumBytes       string            `json:"numBytes"`
}

type state struct {
	Datasets map[string]*dataset `json:"datasets"` // key: project:datasetId
	Tables   map[string]*table   `json:"tables"`   // key: project:datasetId:tableId
}

type Service struct {
	mu sync.RWMutex
	st state
}

func New() *Service {
	s := &Service{
		st: state{
			Datasets: make(map[string]*dataset),
			Tables:   make(map[string]*table),
		},
	}
	_ = storage.Load(serviceName, "state", &s.st)
	if s.st.Datasets == nil {
		s.st.Datasets = make(map[string]*dataset)
	}
	if s.st.Tables == nil {
		s.st.Tables = make(map[string]*table)
	}
	return s
}

func (s *Service) Name() string { return serviceName }

func (s *Service) Meta() service.Meta {
	return service.Meta{
		Display:     "BigQuery",
		Category:    "Analytics & ML",
		Description: "Serverless data warehouse and SQL analytics",
		State:       service.StateBehavioral,
		Fidelity:    service.FidelityB,
	}
}

func (s *Service) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return storage.Save(serviceName, "state", s.st)
}

func (s *Service) RegisterRoutes(r service.Router) {
	// Datasets
	r.Handle("POST", "/bigquery/v2/projects/{project}/datasets", s.createDataset)
	r.Handle("GET", "/bigquery/v2/projects/{project}/datasets", s.listDatasets)
	r.Handle("GET", "/bigquery/v2/projects/{project}/datasets/{dataset}", s.getDataset)
	r.Handle("DELETE", "/bigquery/v2/projects/{project}/datasets/{dataset}", s.deleteDataset)

	// Tables
	r.Handle("POST", "/bigquery/v2/projects/{project}/datasets/{dataset}/tables", s.createTable)
	r.Handle("GET", "/bigquery/v2/projects/{project}/datasets/{dataset}/tables", s.listTables)
	r.Handle("GET", "/bigquery/v2/projects/{project}/datasets/{dataset}/tables/{table}", s.getTable)
	r.Handle("DELETE", "/bigquery/v2/projects/{project}/datasets/{dataset}/tables/{table}", s.deleteTable)

	// Queries & Jobs
	r.Handle("POST", "/bigquery/v2/projects/{project}/queries", s.query)
	r.Handle("POST", "/bigquery/v2/projects/{project}/jobs", s.createJob)
}

func (s *Service) createDataset(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")

	var req struct {
		DatasetReference struct {
			DatasetID string `json:"datasetId"`
			ProjectID string `json:"projectId"`
		} `json:"datasetReference"`
		Location string `json:"location"`
	}
	_ = httpx.DecodeJSON(r, &req)

	dsID := req.DatasetReference.DatasetID
	if dsID == "" {
		dsID = httpx.ID(8)
	}

	key := fmt.Sprintf("%s:%s", project, dsID)
	fullName := fmt.Sprintf("%s:%s", project, dsID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Datasets[key]; exists {
		httpx.AlreadyExists(w, fmt.Sprintf("Dataset %s already exists", key))
		return
	}

	loc := req.Location
	if loc == "" {
		loc = "US"
	}

	ds := &dataset{
		Kind: "bigquery#dataset",
		ID:   fullName,
		DatasetReference: map[string]string{
			"datasetId": dsID,
			"projectId": project,
		},
		Location:     loc,
		CreationTime: httpx.Now(),
	}
	s.st.Datasets[key] = ds
	httpx.WriteJSON(w, http.StatusOK, ds)
}

func (s *Service) listDatasets(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	prefix := project + ":"

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*dataset
	for key, ds := range s.st.Datasets {
		if strings.HasPrefix(key, prefix) {
			result = append(result, ds)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":     "bigquery#datasetList",
		"datasets": result,
	})
}

func (s *Service) getDataset(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")
	key := fmt.Sprintf("%s:%s", project, dsID)

	s.mu.RLock()
	ds, exists := s.st.Datasets[key]
	s.mu.RUnlock()

	if !exists {
		httpx.NotFound(w, fmt.Sprintf("Dataset %s not found", key))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ds)
}

func (s *Service) deleteDataset(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")
	key := fmt.Sprintf("%s:%s", project, dsID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Datasets[key]; !exists {
		httpx.NotFound(w, fmt.Sprintf("Dataset %s not found", key))
		return
	}

	delete(s.st.Datasets, key)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) createTable(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")

	var req struct {
		TableReference struct {
			TableID   string `json:"tableId"`
			DatasetID string `json:"datasetId"`
			ProjectID string `json:"projectId"`
		} `json:"tableReference"`
	}
	_ = httpx.DecodeJSON(r, &req)

	tblID := req.TableReference.TableID
	if tblID == "" {
		tblID = httpx.ID(8)
	}

	key := fmt.Sprintf("%s:%s:%s", project, dsID, tblID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Tables[key]; exists {
		httpx.AlreadyExists(w, fmt.Sprintf("Table %s already exists", key))
		return
	}

	tbl := &table{
		Kind: "bigquery#table",
		ID:   key,
		TableReference: map[string]string{
			"projectId": project,
			"datasetId": dsID,
			"tableId":   tblID,
		},
		CreationTime: httpx.Now(),
		NumRows:      "0",
		NumBytes:     "0",
	}
	s.st.Tables[key] = tbl
	httpx.WriteJSON(w, http.StatusOK, tbl)
}

func (s *Service) listTables(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")
	prefix := fmt.Sprintf("%s:%s:", project, dsID)

	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*table
	for key, tbl := range s.st.Tables {
		if strings.HasPrefix(key, prefix) {
			result = append(result, tbl)
		}
	}

	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind":   "bigquery#tableList",
		"tables": result,
	})
}

func (s *Service) getTable(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")
	tblID := r.PathValue("table")
	key := fmt.Sprintf("%s:%s:%s", project, dsID, tblID)

	s.mu.RLock()
	tbl, exists := s.st.Tables[key]
	s.mu.RUnlock()

	if !exists {
		httpx.NotFound(w, fmt.Sprintf("Table %s not found", key))
		return
	}

	httpx.WriteJSON(w, http.StatusOK, tbl)
}

func (s *Service) deleteTable(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	dsID := r.PathValue("dataset")
	tblID := r.PathValue("table")
	key := fmt.Sprintf("%s:%s:%s", project, dsID, tblID)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.st.Tables[key]; !exists {
		httpx.NotFound(w, fmt.Sprintf("Table %s not found", key))
		return
	}

	delete(s.st.Tables, key)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{})
}

func (s *Service) query(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	jobID := "job_" + httpx.ID(12)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind": "bigquery#queryResponse",
		"jobReference": map[string]string{
			"projectId": project,
			"jobId":     jobID,
		},
		"jobComplete":         true,
		"totalBytesProcessed": "1024",
		"schema": map[string]any{
			"fields": []map[string]string{
				{"name": "id", "type": "STRING"},
				{"name": "status", "type": "STRING"},
			},
		},
		"rows": []map[string]any{
			{"f": []map[string]string{{"v": "1"}, {"v": "OK"}}},
		},
	})
}

func (s *Service) createJob(w http.ResponseWriter, r *http.Request) {
	project := r.PathValue("project")
	jobID := "job_" + httpx.ID(12)

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"kind": "bigquery#job",
		"jobReference": map[string]string{
			"projectId": project,
			"jobId":     jobID,
		},
		"status": map[string]string{
			"state": "DONE",
		},
	})
}
