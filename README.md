<div align="center">

# kiri-gcp 霧

**The cloud, brought down to your machine.**

[![CI](https://img.shields.io/github/actions/workflow/status/Brilhante29/kiri-gcp/ci.yml?branch=main&label=CI&logo=github)](https://github.com/Brilhante29/kiri-gcp/actions)
[![Release](https://img.shields.io/github/v/release/Brilhante29/kiri-gcp?logo=github&label=release&sort=semver)](https://github.com/Brilhante29/kiri-gcp/releases/latest)
[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![OpenSSF Scorecard](https://api.securityscorecards.dev/projects/github.com/Brilhante29/kiri-gcp/badge)](https://securityscorecards.dev/viewer/?uri=github.com/Brilhante29/kiri-gcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/Brilhante29/kiri-gcp)](https://goreportcard.com/report/github.com/Brilhante29/kiri-gcp)

A single binary that emulates **108 Google Cloud services** on one local endpoint.
Real client compatible, offline, free. Point your Go, Python, Node, or Java SDK at it.
Point gcloud, Terraform, or plain REST at it. Build, test, and price a whole GCP architecture
without a project, a credential, or a bill.

`kiri` (霧) is fog: that same cloud at ground level, running locally on your laptop.

**Part of the kiri family** — [**kiri-aws**](https://github.com/Brilhante29/kiri-aws)
does the same for AWS, with the same CLI shape, the same `KIRI_*` configuration,
and the same release guarantees.

</div>

---

## Why kiri

Cloud test environments are slow, shared, and billed. Test suites that touch
Cloud Storage, Pub/Sub, or Firestore end up mocked into meaninglessness, or they
run against a real project and become flaky and expensive. `kiri-gcp` gives you
the real client protocol on `localhost`: your SDK calls are unchanged, the wire
format is the real one, and the whole surface starts in a container in under a
second.

It also answers a question mocks cannot: **what will this architecture cost?**
A pricing catalog and a cost query let you price a design locally, before any of
it exists.

- **108 Google Cloud services in one binary** — Cloud Storage, Pub/Sub,
  Firestore, BigQuery, Cloud Run, Secret Manager, IAM, KMS, Spanner, GKE,
  Cloud SQL, and 97 more.
- **No credentials, no project.** Runs in zero-auth mode; nothing leaves the machine.
- **Works with the tools you already use** — Google Cloud SDKs (Go, Python, Node,
  Java), `gcloud`, and Terraform, by overriding one endpoint.
- **Two transports** — REST/JSON on `:4443` and native gRPC on `:8085` for
  Pub/Sub and Firestore streaming.
- **Cost surface** — a pricing catalog plus `/kiri/billing/seed` and
  `/kiri/billing/cost` to project a monthly bill.
- **In-process Go testing** — `kiri.NewServer()` runs the emulator inside your
  test binary.
- **Optional persistence** — set `KIRI_DATA_DIR` and state survives restarts.

---

## Install

Every release ships signed binaries for linux, macOS, and Windows (amd64 and
arm64), a multi-arch container image, an SBOM, and SLSA build provenance.

```bash
# Container (recommended)
docker run -d -p 4443:4443 -p 8085:8085 --name kiri ghcr.io/brilhante29/kiri-gcp:latest

# Go toolchain
go install github.com/Brilhante29/kiri-gcp/cmd/kiri@latest
```

Or grab a binary from the [latest release](https://github.com/Brilhante29/kiri-gcp/releases/latest).
See [RELEASING.md](RELEASING.md) to verify signatures and provenance.

---

## Quickstart

### Option A: Run the published image (recommended)

```bash
docker run -d -p 4443:4443 -p 8085:8085 --name kiri ghcr.io/brilhante29/kiri-gcp:latest
```

To build from a checkout instead: `docker build -t kiri -f docker/Dockerfile .`

Verify that the emulator is running:

```bash
curl http://localhost:4443/
# {"emulator":"kiri","status":"ok","services":108,"grpc_port":8085}
```

### Option B: Docker Compose

A [`docker-compose.yml`](docker-compose.yml) ships with the repository:

```bash
docker compose up -d
```

### Option C: Go module (in-process testing)

Import the module and run the emulator inside your test binary — no container, no
port juggling, and it shuts down with the test:

```go
import (
    "cloud.google.com/go/storage"
    kiri "github.com/Brilhante29/kiri-gcp"
    "google.golang.org/api/option"
)

func TestUploadsReport(t *testing.T) {
    srv := kiri.NewServer() // random port on localhost
    defer srv.Close()

    client, err := storage.NewClient(t.Context(),
        option.WithEndpoint(srv.URL),
        option.WithoutAuthentication(),
    )
    // ... exercise the code under test against client
}
```

Or run it straight from a checkout:

```bash
go run ./cmd/kiri --http-port 4443
```

---

## Language & tooling setup

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

## Cost surface (Cost Explorer analogue)

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

## Supported services

108 services are registered and reachable on the same endpoints. The running
emulator reports the live count:

```bash
curl -s http://localhost:4443/
# {"emulator":"kiri","status":"ok","services":108,"grpc_port":8085}
```

The canonical list is [`internal/registry/registry.go`](internal/registry/registry.go):
every service registers itself there through an `init()` hook, so the registry and
the binary can never disagree.

---

## Configuration

| Variable | Default | Description |
|---|---|---|
| `KIRI_HOST` | `0.0.0.0` | Bind address |
| `KIRI_HTTP_PORT` | `4443` | REST / JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port (Pub/Sub, Firestore) |
| `KIRI_DATA_DIR` | *(unset)* | Directory for state snapshots (enables persistence) |
| `KIRI_LOG_LEVEL` | `info` | Logging verbosity: `debug`, `info`, `warn`, `error` |
| `KIRI_DEBUG_STREAMINGPULL` | *(unset)* | Enables verbose gRPC Pub/Sub streaming pull tracing |

---

## Architecture

```
                 ┌──────────────────────────────────────────────┐
                 │                 kiri Server                  │
                 ├──────────────────────┬───────────────────────┤
                 │   REST Mux (:4443)   │  gRPC Server (:8085)  │
                 └──────────┬───────────┴───────────┬───────────┘
                            │                       │
                            ▼                       ▼
                 ┌──────────────────────────────────────────────┐
                 │           Unified Service Registry           │
                 │                (108 Services)                │
                 └──────────────────────┬───────────────────────┘
                                        │
                        ┌───────────────┴───────────────┐
                        ▼                               ▼
            ┌───────────────────────┐      ┌────────────────────────┐
            │  Pricing catalog +    │      │  Storage + persistence │
            │  cost query           │      │  ($KIRI_DATA_DIR)      │
            └───────────────────────┘      └────────────────────────┘
```

One Go process fronts every service: an `http.ServeMux` serves REST/JSON traffic
and a gRPC server handles Pub/Sub and Firestore streaming. Services register
themselves through `init()` hooks and share the same in-memory state, which is
snapshotted to disk when `KIRI_DATA_DIR` is set.

---

## Examples

Runnable samples live in [`examples/`](./examples):

- **[`examples/go/`](./examples/go)** — Go SDK connection, bucket creation, object upload, Pub/Sub topics.
- **[`examples/python/`](./examples/python)** — Python SDK against the Storage and Pub/Sub emulators.
- **[`examples/nodejs/`](./examples/nodejs)** — Node.js `@google-cloud` SDK usage.
- **[`examples/terraform/`](./examples/terraform)** — Terraform manifest pointed at `kiri`.
- **[`examples/testing/`](./examples/testing)** — in-process Go test suite using `kiri.NewServer()`.
- **[`examples/scenario/`](./examples/scenario)** — end-to-end run that provisions resources and prices them.
- **[`examples/architectures/`](./examples/architectures)** — blueprints for serverless APIs, data lakes, and microservices with monthly cost simulations.

---

## Releases and supply chain

Releases are automated end to end — see [RELEASING.md](RELEASING.md). Every tag
publishes signed binaries (linux, macOS, Windows on amd64/arm64), checksums
signed keylessly with [cosign](https://sigstore.dev), an SBOM per archive, SLSA
build provenance, and a multi-arch image on `ghcr.io`.

```bash
cosign verify ghcr.io/brilhante29/kiri-gcp:latest \
  --certificate-identity-regexp '^https://github.com/Brilhante29/kiri-gcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

---

## Contributing

Issues and pull requests are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md)
first: it covers the layout, how to add a service, and the local checks. Pull
request titles follow [Conventional Commits](https://www.conventionalcommits.org)
because the release automation derives the next version from them.

---

## License

[MIT](LICENSE) © 2026 Guilherme Brilhante and the kiri-gcp contributors.
