<div align="center">

# kiri 霧

**The cloud, brought down to your machine.**

[![CI](https://img.shields.io/github/actions/workflow/status/Brilhante29/kiri-gcp/ci.yml?branch=main&label=CI&logo=github)](https://github.com/Brilhante29/kiri-gcp/actions)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/Brilhante29/kiri-gcp/badge)](https://securityscorecards.dev/viewer/?uri=github.com/Brilhante29/kiri-gcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/Brilhante29/kiri-gcp)](https://goreportcard.com/report/github.com/Brilhante29/kiri-gcp)

A single binary that emulates **108 Google Cloud services** on one local endpoint.
Real client compatible, offline, free. Point your Go, Python, Node, or Java SDK at it.
Point gcloud, Terraform, or plain REST at it. Build, test, and price a whole GCP architecture
without a project, a credential, or a bill.

`kiri` (霧) is fog: that same cloud at ground level, running locally on your laptop.

</div>

---

## ⚡ Features & Value Proposition

- **108 GCP Services in 1 Binary:** Cloud Storage, Pub/Sub, Firestore, BigQuery, Cloud Run, Secret Manager, IAM, KMS, Spanner, GKE, Cloud SQL, and 97 more.
- **Zero Credentials Needed:** Runs in zero-auth mode locally. Override endpoints without service accounts, IAM keys, or cloud billing accounts.
- **Multi-Protocol Support:** Dual REST/JSON (`:4443`) and native gRPC (`:8085`) transports compatible with official Google Cloud SDKs.
- **Integrated Cost Surface:** A pricing catalog plus `/kiri/billing/cost` and `/kiri/billing/seed` endpoints to project monthly GCP bills and price cloud architectures locally, the Cost Explorer analogue.
- **In-Process Go Testing:** Import `github.com/Brilhante29/kiri-gcp` directly in your Go test suite using `kiri.NewServer()` for lightning-fast, isolated unit/integration tests without external dependencies.
- **Optional Data Persistence:** Configure `$KIRI_DATA_DIR` to save and restore local emulator states across container restarts.

---

## 🚀 Quickstart

### Option A: Run via Docker (Recommended)

Build the image from source and run it:

```bash
docker build -t kiri -f docker/Dockerfile .
docker run -d -p 4443:4443 -p 8085:8085 --name kiri kiri
```

Verify that the emulator is running:

```bash
curl http://localhost:4443/
# {"emulator":"kiri","status":"ok","services":108,"grpc_port":8085}
```

### Option B: Docker Compose

```yaml
services:
  kiri:
    build:
      context: .
      dockerfile: docker/Dockerfile
    ports:
      - "4443:4443"
      - "8085:8085"
    environment:
      KIRI_HOST: "0.0.0.0"
      KIRI_HTTP_PORT: "4443"
      KIRI_GRPC_PORT: "8085"
      KIRI_LOG_LEVEL: "info"
      KIRI_DATA_DIR: "/data"
    volumes:
      - kiri-data:/data

volumes:
  kiri-data:
```

### Option C: Go Module (In-Process Testing)

```go
package main

import (
    "context"
    "fmt"
    "cloud.google.com/go/storage"
    "github.com/Brilhante29/kiri-gcp"
    "google.golang.org/api/option"
)

func main() {
    // Start an in-process kiri emulator on random ports
    srv := kiri.NewServer()
    defer srv.Close()

    // Point any Go GCP client to the emulator
    client, _ := storage.NewClient(context.Background(),
        option.WithEndpoint(srv.URL),
        option.WithoutAuthentication(),
    )
    
    fmt.Println("Connected to local kiri server at:", srv.URL)
}
```

---

## 💻 Language & Tooling Setup

### Go SDK
```go
import (
    "cloud.google.com/go/storage"
    "google.golang.org/api/option"
)

client, err := storage.NewClient(ctx,
    option.WithEndpoint("http://localhost:4443"),
    option.WithoutAuthentication(),
)
```

### Python SDK
```bash
export STORAGE_EMULATOR_HOST="http://localhost:4443"
export PUBSUB_EMULATOR_HOST="localhost:8085"
```
```python
from google.cloud import storage

# Automatically routes requests to local kiri instance
client = storage.Client()
```

### Node.js / TypeScript SDK
```bash
export PUBSUB_EMULATOR_HOST="localhost:8085"
```
```javascript
const {Storage} = require('@google-cloud/storage');

const storage = new Storage({
  apiEndpoint: 'http://localhost:4443',
});
```

### Terraform
```hcl
provider "google" {
  project     = "local-project"
  region      = "us-central1"
  access_token = "dummy"

  storage_custom_endpoint = "http://localhost:4443/storage/v1/"
  pubsub_custom_endpoint  = "http://localhost:4443/v1/"
  secret_manager_custom_endpoint = "http://localhost:4443/v1/"
}
```

### gcloud CLI
```bash
gcloud config set auth/disable_credentials true
gcloud config set api_endpoint_overrides/storage http://localhost:4443/
```

---

## 💰 Cost surface (Cost Explorer analogue)

`kiri` carries a pricing catalog (Compute, Storage, BigQuery SKUs) and a cost
query so you can project what a GCP architecture would cost, locally. Seed cost
line items, then query them grouped by service, SKU, or project over a window.

Seed usage:

```bash
curl -X POST http://localhost:4443/kiri/billing/seed \
  -H "Content-Type: application/json" \
  -d '[
    {"service":"Compute Engine","sku":"N1 Predefined vCPU running","project":"my-project","cost":46.15,"usageStart":"2026-07-01","usageEnd":"2026-08-01"},
    {"service":"Cloud Storage","sku":"Standard Storage US","project":"my-project","cost":0.10,"usageStart":"2026-07-01","usageEnd":"2026-08-01"}
  ]'
```

Query the cost, grouped by service:

```bash
curl -X POST http://localhost:4443/kiri/billing/cost \
  -H "Content-Type: application/json" \
  -d '{"groupBy":"service"}'
```

**Response:**
```json
{
  "groupBy": "service",
  "currency": "USD",
  "total": 46.25,
  "groups": [
    { "key": "Cloud Storage", "cost": 0.10, "currency": "USD" },
    { "key": "Compute Engine", "cost": 46.15, "currency": "USD" }
  ]
}
```

For a full end-to-end example that provisions real resources through the Google
SDKs and projects their monthly cost, see
[`examples/scenario`](examples/scenario).

---

## 📁 Examples Directory (`examples/`)

Explore complete code samples and architecture blueprints in the [`examples/`](./examples) directory:

- **[`examples/go/`](./examples/go):** Go SDK connection, bucket creation, object upload, Pub/Sub topic handling.
- **[`examples/python/`](./examples/python):** Python SDK integration with Storage and Pub/Sub emulators.
- **[`examples/nodejs/`](./examples/nodejs):** Node.js `@google-cloud` SDK usage.
- **[`examples/terraform/`](./examples/terraform):** Terraform HCL manifest directing resources to `kiri`.
- **[`examples/testing/`](./examples/testing):** In-process Go integration test suite using `kiri.NewServer()`.
- **[`examples/architectures/`](./examples/architectures):** Architectural blueprints for Serverless APIs, Data Lakes, and Microservices with monthly cost simulations.

---

## ⚙️ Environment Variables

| Variable | Default | Description |
|---|---|---|
| `KIRI_HOST` | `0.0.0.0` | Bind address |
| `KIRI_HTTP_PORT` | `4443` | REST / JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port (Pub/Sub, Firestore) |
| `KIRI_DATA_DIR` | *(unset)* | Directory for state snapshots (enables persistence) |
| `KIRI_LOG_LEVEL` | `info` | Logging verbosity: `debug`, `info`, `warn`, `error` |
| `KIRI_DEBUG_STREAMINGPULL` | *(unset)* | Enables verbose gRPC Pub/Sub streaming pull tracing |

---

## 🏛️ Architecture Overview

```
                        ┌──────────────────────────────────────────────┐
                        │                 kiri Server                  │
                        ├──────────────────────┬───────────────────────┤
                        │   REST Mux (:4443)   │   gRPC Server (:8085) │
                        └──────────┬───────────┴───────────┬───────────┘
                                   │                       │
                                   ▼                       ▼
                        ┌──────────────────────────────────────────────┐
                        │          Unified Service Registry            │
                        │               (108 Services)                 │
                        └──────────────────────┬───────────────────────┘
                                               │
                                               ▼
                        ┌──────────────────────────────────────────────┐
                        │      Atomic Storage Persistence ($KIRI_DATA_DIR) │
                        └──────────────────────────────────────────────┘
```

`kiri` runs a single Go process housing an `http.ServeMux` for REST/JSON traffic and a gRPC server for Pub/Sub and Firestore streaming operations. All services register via Go `init()` hooks and share unified memory states.

---

## 📄 License

[MIT](LICENSE)
