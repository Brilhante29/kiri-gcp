# kiri: Design Spec

**Date:** 2026-07-20
**Status:** Approved, in implementation
**Author:** kiri

## Summary

`kiri` is a lightweight **Google Cloud Platform (GCP) service emulator** written in
Go, mirroring the architecture of [`kumo`](https://github.com/sivchari/kumo) (which
emulates AWS). Same value proposition: no authentication, single binary, Docker-first,
fast startup, optional data persistence, but for GCP APIs and client libraries instead
of AWS.

It is a **sibling project**, independent of the AWS `kumo` codebase.

## Goals

- Emulate the **essential GCP services** over their REST/JSON transport, so Go (and any
  language) Google Cloud client libraries work against it with only an endpoint override.
- Provide the **most complete cost/billing surface** possible: Cloud Billing accounts,
  project billing info, the pricing catalog (services + SKUs), Billing Budgets, and a
  synthetic cost-query surface analogous to AWS Cost Explorer's `GetCostAndUsage`.
- Mirror `kumo`'s internal design: central service registry, `Meta()`-driven README
  catalog, atomic JSON persistence, graceful shutdown, init-scripts directory.
- **Docker-first**: build and run exclusively via Docker (multi-stage build, no host Go
  toolchain required).

## Non-goals

- Real authentication / IAM policy enforcement (emulator is zero-auth like `kumo`).
- Byte-for-byte fidelity with production GCP quotas, pagination limits, or error codes.
- Full gRPC coverage beyond Pub/Sub and Firestore (Wave 2). REST covers all 21 services;
  clients use `option.WithEndpoint` + `option.WithoutAuthentication`.

## Compatibility model (how clients connect)

GCP client libraries reach the emulator two ways:

1. **Official emulator env vars** (honored where GCP defines them):
   - `STORAGE_EMULATOR_HOST` → Cloud Storage (fully drop-in with the Go `storage` client).
   - `PUBSUB_EMULATOR_HOST`, `FIRESTORE_EMULATOR_HOST` → gRPC endpoints (Wave 2). REST
     Pub/Sub / Firestore are reachable now via endpoint override.
2. **Endpoint override** for everything else:
   `option.WithEndpoint("http://localhost:4443")` + `option.WithoutAuthentication()`.

Zero-auth: the server ignores `Authorization` headers entirely, like `kumo`.

## Configuration (environment variables)

| Variable | Default | Description |
|----------|---------|-------------|
| `KIRI_HOST` | `0.0.0.0` | Server bind address |
| `KIRI_HTTP_PORT` | `4443` | REST/JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port (Wave 2) |
| `KIRI_LOG_LEVEL` | `info` | `debug`\|`info`\|`warn`\|`error` |
| `KIRI_DATA_DIR` | (unset) | Persistent storage dir; in-memory only when unset |
| `KIRI_INIT_DIR` | (unset) | Directory of `.sh`/`.json` init scripts run on startup |

## Architecture

```
kiri/
  gcp.go                     # public in-process API: NewServer() -> httptest.Server
  cmd/kiri/main.go       # binary entrypoint + minimal flag CLI (stdlib)
  cmd/readme-gen/main.go     # regenerate README service catalog from Meta()
  internal/
    service/interface.go     # Service, Meta, Describer, RESTService, GRPCService
    registry/registry.go     # canonical blank-import list of all services
    catalog/catalog.go       # render README "Supported Services" from Meta()
    storage/persistence.go   # atomic JSON load/save (copied pattern from kumo)
    httpx/                    # REST helpers: JSON write, error shape, path vars, IDs
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

Services register concrete `net/http` routes. The server owns one `http.ServeMux`;
Go 1.22 method+pattern routing (`GET /storage/v1/b/{bucket}`, wildcards, `{path...}`)
replaces `kumo`'s custom router for REST. GCP path structure is regular, so no protocol
dispatcher is needed (unlike AWS JSON/Query/CBOR).

### Persistence

Identical to `kumo`: each service implementing `io.Closer` saves to
`$KIRI_DATA_DIR/{service}.json` on shutdown; loads on boot; atomic tmp+rename writes.

## Cost / Billing: the "most complete" surface

Four cooperating surfaces under the `billing` + `costexplorer` packages:

1. **Cloud Billing** (`cloudbilling.googleapis.com/v1`):
   `billingAccounts` list/get/create, `projects/{p}/billingInfo` get/update,
   `services` list, `services/{s}/skus` list (real pricing catalog seed).
2. **Billing Budgets** (`billingbudgets.googleapis.com/v1`):
   `billingAccounts/{a}/budgets` CRUD with threshold rules.
3. **Cost query** (kiri-specific, analogous to AWS `GetCostAndUsage`):
   `POST /kumo/billing/cost`: returns line items grouped by service / SKU / project /
   label over a time window, with filters. GCP has no native cost API (production uses
   BigQuery billing export), so this is a first-class emulator convenience.
4. **Detailed billing export**: a seedable, queryable detailed-cost table mirroring the
   BigQuery billing-export schema, feeding surface 3.

## Service catalog (essential GCP services)

Categories mirror `kumo`'s catalog, adapted to GCP:

- **Storage:** Cloud Storage (GCS), Filestore
- **Compute:** Compute Engine, Cloud Run, Cloud Functions, App Engine Admin
- **Containers:** GKE, Artifact Registry
- **Database:** Firestore, Datastore, Cloud SQL, Bigtable, Spanner, Memorystore (Redis)
- **Analytics & ML:** BigQuery, Dataflow, Dataproc, Vertex AI
- **Messaging & Integration:** Pub/Sub, Cloud Tasks, Cloud Scheduler, Eventarc, Workflows
- **Security & Identity:** IAM, Secret Manager, Cloud KMS, Resource Manager
- **Networking:** Cloud DNS, Compute Networks (VPC), Load Balancing
- **Monitoring & Logging:** Cloud Logging, Cloud Monitoring, Error Reporting
- **Management & Billing:** Cloud Billing, Billing Budgets, Cost Query, Service Usage

## Build order (waves)

- **Wave 1 (foundation, this session):** scaffold + full REST core:
  Cloud Storage, Cloud Billing, Budgets, Cost query, Secret Manager, Cloud KMS, IAM,
  Resource Manager, Pub/Sub (REST), Cloud Tasks, Cloud Scheduler, Cloud Run,
  Cloud Functions, Firestore (REST), BigQuery, Cloud Logging, Cloud DNS, Compute Engine,
  GKE, Cloud SQL, Cloud Monitoring, Artifact Registry, Service Usage, Pub/Sub-Lite (skip).
  Docker build verified via `docker build`.
- **Wave 2 (complete):** native gRPC transport (grpc-go + custom protow codec) for
  Pub/Sub & Firestore. `PUBSUB_EMULATOR_HOST` / `FIRESTORE_EMULATOR_HOST` env vars work
  unchanged. gRPC server runs on KIRI_GRPC_PORT (default 8085), shared state with the
  REST layer via MergeBackend adapters. Firestore REST API added.
- **Wave 3 (complete):** 12 remaining services implemented: BigQuery, Cloud Run,
  Compute Engine, Cloud DNS, Cloud Functions, Cloud SQL, Cloud Tasks, Cloud Scheduler,
  GKE, Logging, Monitoring, Artifact Registry. All registered in the canonical blank-import
  registry. Total ~8,819 lines of Go across 47 source files.
- **Wave 4 (complete):** cross-service wiring:
  - GCS → Pub/Sub notifications (notification configs API + PublishFunc callback)
  - Cloud Scheduler → HTTP/Pub/Sub targets (runJob executes the configured target)
  - Eventarc service (triggers + channels, full CRUD)
  - Unified REST/gRPC state sharing (single MergeBackend shared by REST and gRPC Pub/Sub)
  - Integration tests for all 12 Wave 3 services + Eventarc (22 test functions)
- **Wave 5+:** deeper operation coverage; Datastore mode for Firestore; more gRPC services;
  cross-service events via Eventarc; Cloud Scheduler cron execution on schedule.

## Testing

- In-process integration tests via `gcp.NewServer()` (mirrors `kumo.NewServer()`), driven
  with `net/http` and, where available, the real Google Cloud Go client libraries pointed
  at the test server.
- `docker build` is the compile gate (no host Go). A smoke test runs the container and
  curls representative endpoints.
