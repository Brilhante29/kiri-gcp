# 🏛️ Architecture Blueprints & Cost Simulation Examples

This directory provides architecture blueprints and cost estimation models powered by `kiri`'s **Cost Engine** (`/kiri/billing/calculator`).

---

## 1. Serverless Microservice API (`serverless_api.json`)

**Components:**
- **Cloud Run:** Handles 10M API requests/month with 2 vCPUs and 4GB RAM.
- **Pub/Sub:** Handles 25M event messages/month for asynchronous background processing.
- **Firestore:** Document database with 500k reads/day and 200k writes/day.
- **Cloud Storage:** Stores 250GB of media files and assets.

### How to Calculate Cost using `curl`:

```bash
curl -X POST http://localhost:4443/kiri/billing/calculator \
  -H "Content-Type: application/json" \
  -d @serverless_api.json
```

---

## 2. BigData & AI Analytics Pipeline (`bigdata_analytics.json`)

**Components:**
- **BigQuery:** 2TB data scanned and processed per month.
- **Vertex AI:** 500k input tokens + 200k output tokens processed per day.
- **Cloud Storage:** Data lake storing 1TB of raw parquet files.

### How to Calculate Cost:

```bash
curl -X POST http://localhost:4443/kiri/billing/calculator \
  -H "Content-Type: application/json" \
  -d '{
    "resources": [
      { "service": "bigquery", "queryBytesTB": 2.0, "storageGb": 500.0 },
      { "service": "vertexai", "inputTokensK": 500, "outputTokensK": 200 },
      { "service": "storage", "storageGb": 1000.0 }
    ]
  }'
```

---

## 3. High-Availability Microservices Cluster

**Components:**
- **GKE (Google Kubernetes Engine):** 3 nodes (n2-standard-4: 4 vCPUs, 16GB RAM each).
- **Cloud SQL (PostgreSQL):** Regional HA instance with 100GB storage.
- **Memorystore (Redis):** 10GB in-memory cache instance.

### How to Calculate Cost:

```bash
curl -X POST http://localhost:4443/kiri/billing/calculator \
  -H "Content-Type: application/json" \
  -d '{
    "resources": [
      { "service": "gke", "nodes": 3, "cpu": 4.0, "memoryGb": 16.0 },
      { "service": "cloudsql", "ha": true, "cpu": 4.0, "memoryGb": 16.0, "storageGb": 100.0 },
      { "service": "memorystore", "capacityGb": 10.0 }
    ]
  }'
```
