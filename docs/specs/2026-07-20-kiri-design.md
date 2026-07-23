# kiri: Design Spec

**Date:** 2026-07-20
**Status:** Approved, in implementation
**Author:** kiri

## Summary

`kiri` (霧) is a single-binary **Google Cloud Platform (GCP) service emulator** written in Go. Value proposition: zero authentication required, single binary, Docker-first, fast startup, optional data persistence, and cost calculation engine for GCP APIs and client libraries.

It is a standalone, lightweight local emulator for GCP architecture development, local integration testing, and cost estimation.

## Goals

- Emulate essential GCP services over their REST/JSON and gRPC transports, so Go, Python, Node.js, Java, and Terraform client libraries work against it with only an endpoint override.
- Provide the **most complete cost/billing surface** possible: Cloud Billing accounts, project billing info, the pricing catalog (services + SKUs), Billing Budgets, and a synthetic cost-query and price estimation surface (`/kiri/billing/cost`, `/kiri/billing/calculator`).
- Maintain a clean internal architecture: central service registry, `Meta()`-driven README catalog, atomic JSON persistence, graceful shutdown.
- **Docker-first**: build and run via Docker or single standalone binary.

## Non-goals

- Real authentication / IAM policy enforcement (emulator is zero-auth).
- Byte-for-byte fidelity with production GCP quotas or pagination limits.
- Full gRPC coverage beyond Pub/Sub and Firestore. REST covers all services; clients use `option.WithEndpoint` + `option.WithoutAuthentication`.

## Compatibility model (how clients connect)

GCP client libraries reach the emulator two ways:

1. **Official emulator env vars** (honored where GCP defines them):
   - `STORAGE_EMULATOR_HOST` → Cloud Storage (`http://localhost:4443`).
   - `PUBSUB_EMULATOR_HOST`, `FIRESTORE_EMULATOR_HOST` → gRPC endpoints (`localhost:8085`).
2. **Endpoint override** for everything else:
   `option.WithEndpoint("http://localhost:4443")` + `option.WithoutAuthentication()`.

Zero-auth: the server ignores `Authorization` headers entirely.

## Configuration (environment variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `KIRI_HOST` | `0.0.0.0` | Server bind address |
| `KIRI_HTTP_PORT` | `4443` | REST/JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port |
| `KIRI_LOG_LEVEL` | `info` | `debug`\|`info`\|`warn`\|`error` |
| `KIRI_DATA_DIR` | (unset) | Persistent storage dir; in-memory only when unset |
| `KIRI_INIT_DIR` | (unset) | Directory of `.sh`/`.json` init scripts run on startup |

## Architecture

```
kiri/
  gcp.go                     # public in-process API: NewServer() -> httptest.Server
  cmd/kiri/main.go           # binary entrypoint + minimal flag CLI (stdlib)
  cmd/readme-gen/main.go     # regenerate README service catalog from Meta()
  internal/
    service/interface.go     # Service, Meta, Describer, RESTService, GRPCService
    registry/registry.go     # canonical blank-import list of all services
    catalog/catalog.go       # render README "Supported Services" from Meta()
    storage/persistence.go   # atomic JSON load/save
    httpx/                   # REST helpers: JSON write, error shape, path vars, IDs
    server/
      server.go              # lifecycle: build mux, register services, shutdown
      router.go              # net/http ServeMux (Go 1.22 method+path patterns)
      logging.go             # slog request logging middleware
    service/<name>/          # one package per GCP service
  docker/Dockerfile          # multi-stage build (golang -> distroless/alpine)
  docker-compose.yml
  Makefile, README.md, go.mod
```

### Service interface

```go
type Service interface {
    Name() string                 // "storage", "pubsub", "billing"
    RegisterRoutes(r Router)      // register REST routes on the mux
}
type Describer interface { Meta() Meta }   // README catalog metadata
type Meta struct { Display, Category, Description string }
```

Services register concrete `net/http` routes on `http.ServeMux` using Go 1.22 routing patterns.

### Persistence

Services implementing `io.Closer` save to `$KIRI_DATA_DIR/{service}.json` on shutdown and load on boot using atomic tmp+rename writes.

## Cost / Billing: the "most complete" surface

Four cooperating surfaces under the `billing` package:

1. **Cloud Billing** (`cloudbilling.googleapis.com/v1`):
   `billingAccounts` list/get/create, `projects/{p}/billingInfo` get/update, `services` list, `services/{s}/skus` list.
2. **Billing Budgets** (`billingbudgets.googleapis.com/v1`):
   `billingAccounts/{a}/budgets` CRUD with threshold rules.
3. **Cost query**:
   `POST /kiri/billing/cost`: returns line items grouped by service / SKU / project over a time window.
4. **GCP Price Calculator**:
   `POST /kiri/billing/calculator`: calculates exact monthly estimated costs based on GCP SKU pricing rules.

## Service catalog (108 GCP services)

- **Storage:** Cloud Storage (GCS), Filestore
- **Compute:** Compute Engine, Cloud Run, Cloud Functions, App Engine Admin
- **Containers:** GKE, Artifact Registry
- **Database:** Firestore, Datastore, Cloud SQL, Bigtable, Spanner, Memorystore
- **Analytics & ML:** BigQuery, Dataflow, Dataproc, Vertex AI
- **Messaging & Integration:** Pub/Sub, Cloud Tasks, Cloud Scheduler, Eventarc, Workflows
- **Security & Identity:** IAM, Secret Manager, Cloud KMS, Resource Manager
- **Networking:** Cloud DNS, Compute Networks (VPC), Load Balancing
- **Monitoring & Logging:** Cloud Logging, Cloud Monitoring, Error Reporting
- **Management & Billing:** Cloud Billing, Billing Budgets, Cost Query, Service Usage

## Testing

- In-process integration tests via `gcp.NewServer()`, driven with `net/http` and Go Google Cloud SDK.
- `docker build` compile gate.
