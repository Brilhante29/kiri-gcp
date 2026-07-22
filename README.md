<div align="center">

# kiri 霧

**The cloud, brought down to your machine.**

A single binary that emulates **108 Google Cloud services** on one local endpoint.
Real client compatible, offline, free. Point the Go, Python, Node, or Java SDK at
it. Point gcloud, Terraform, or plain REST at it. Build, test, and *price* a whole
GCP architecture without a project, a credential, or a bill.

`kumo` (雲) is the cloud, up there. `kiri` (霧) is fog: that same cloud at ground
level, running on your laptop.

</div>

## Why kiri exists

Developing against GCP puts you in a bad spot, with two options that both hurt.

**Hit real GCP.** Every test run needs credentials and a network round trip. It
spends real money, pollutes shared projects, rate limits your CI, and cannot run
on a plane or in an air gapped pipeline.

**Use the official emulators.** Google ships a handful (Pub/Sub, Firestore,
Bigtable, Datastore, Spanner). They are separate binaries with separate flags,
they cover maybe five services, and none of them can tell you what your
architecture would cost.

AWS developers got a single local endpoint for everything years ago. GCP
developers never did. kiri is that missing piece: one process, one endpoint, the
whole platform, plus a cost surface no other emulator has.

| | Real GCP | Official emulators | **kiri** |
|---|:---:|:---:|:---:|
| Runs offline | no | yes | **yes** |
| Costs money | yes | free | **free** |
| Services covered | all | ~5 | **108** |
| One binary, one endpoint | n/a | no (5 binaries) | **yes** |
| Works with real clients | yes | yes | **yes** |
| Works with Terraform / gcloud | yes | partial | **yes** |
| Cost / billing prediction | yes | no | **yes** |

## Quick start

Build the image from source and run it. REST on 4443, gRPC on 8085.

```bash
docker build -t kiri -f docker/Dockerfile .
docker run -p 4443:4443 -p 8085:8085 kiri
```

Confirm it is up:

```bash
curl http://localhost:4443/
# {"emulator":"kiri","status":"ok","services":108,"grpc_port":8085}
```

## Point any tool at kiri

Same emulator, seven front doors. Every Google Cloud client accepts a custom
endpoint, so nothing in your app changes but the address.

### Go

```bash
export STORAGE_EMULATOR_HOST=http://localhost:4443
export PUBSUB_EMULATOR_HOST=localhost:8085
```

```go
client, _ := storage.NewClient(ctx,
    option.WithEndpoint("http://localhost:4443/storage/v1/"),
    option.WithoutAuthentication())
```

### Python

```bash
export STORAGE_EMULATOR_HOST=http://localhost:4443
export PUBSUB_EMULATOR_HOST=localhost:8085
```

```python
from google.cloud import storage

client = storage.Client(
    project="my-project",
    client_options={"api_endpoint": "http://localhost:4443"})
```

### Node.js

```bash
export PUBSUB_EMULATOR_HOST=localhost:8085
```

```js
const {Storage} = require('@google-cloud/storage');

const storage = new Storage({
  projectId: 'my-project',
  apiEndpoint: 'http://localhost:4443',
});
```

### Java

```java
Storage storage = StorageOptions.newBuilder()
    .setHost("http://localhost:4443")
    .setProjectId("my-project")
    .build()
    .getService();
```

Pub/Sub reads `PUBSUB_EMULATOR_HOST` the same as every other language.

### gcloud CLI

```bash
gcloud config set auth/disable_credentials true
gcloud config set api_endpoint_overrides/storage http://localhost:4443/storage/v1/
gcloud storage buckets list
```

### Terraform

```hcl
provider "google" {
  project = "my-project"

  storage_custom_endpoint = "http://localhost:4443/storage/v1/"
  compute_custom_endpoint = "http://localhost:4443/compute/v1/"
  pubsub_custom_endpoint  = "http://localhost:4443/v1/"
}
```

Set `GOOGLE_OAUTH_ACCESS_TOKEN=dummy` so the provider skips real auth.

### REST

```bash
curl -X POST "http://localhost:4443/storage/v1/b?project=my-project" \
  -H 'Content-Type: application/json' \
  -d '{"name":"my-bucket"}'
```

## Price your architecture

kiri ships a Cost surface, the GCP analogue of AWS Cost Explorer. It carries a
real pricing catalog (Compute, Storage, BigQuery SKUs with actual unit prices),
budgets, and a cost query grouped by service, SKU, or project over a time window.

You can stand up an architecture locally and predict what it would cost in
production before spending a cent. Here is a real run of the included scenario (an
order ingestion pipeline: Storage to Pub/Sub to a Compute worker), driven
end to end by the real Google Cloud client libraries:

```
STEP 5. Billing: link account, PREDICT monthly cost from live catalog
   ok  fetched live SKU prices: vCPU=$0.031611/h RAM=$0.004237/GiB.h GCS=$0.020/GiB.mo
   predicted monthly cost:
     Compute Engine  n1-standard-2 (730h)  = $   69.35
     Cloud Storage   5 GiB-mo              = $    0.10
     projected total                       = $   69.45 / month
   ok  cost query total $69.45 matches projection $69.45
   ok  projected spend $69.45 is within the $200 budget (34.7%)
```

The projection is not hardcoded. It reads unit prices out of kiri's live catalog
and multiplies by your provisioned resources. Change the machine type, re-run, get
a new number. Wire it into CI to fail a PR that would blow the budget. The full
runnable source lives in [`examples/scenario`](examples/scenario).

## What's covered

108 services across 13 categories.

| Category | Count | Examples |
|---|:---:|---|
| Analytics & ML | 16 | BigQuery, Dataflow, Dataproc, Vertex AI, Natural Language |
| Databases | 15 | Cloud SQL, Spanner, Bigtable, Firestore, AlloyDB, Memorystore |
| Security | 13 | IAM, Secret Manager, KMS, Binary Authorization, Cloud SCC |
| Networking | 11 | Load Balancing, Cloud DNS, Private Connect, Network Connectivity |
| Management & Billing | 8 | Cloud Billing + Cost, Resource Manager, Service Usage |
| Compute | 7 | Compute Engine, Cloud Run, Cloud Functions, App Engine, Batch |
| Storage | 6 | Cloud Storage, Persistent Disk, Storage Control, Archive |
| Containers | 6 | GKE, GKE Autopilot, Artifact Registry, Cloud Service Mesh |
| Application Integration | 6 | Workflows, Eventarc, API Gateway, Cloud Scheduler, Cloud Tasks |
| Messaging & Integration | 5 | Pub/Sub, Managed Kafka, FCM, Service Directory |
| Monitoring & Logging | 5 | Cloud Monitoring, Cloud Logging, Error Reporting, Cloud Trace |
| Developer Tools | 5 | Cloud Build, Cloud Deploy, Cloud Composer |
| Other Services | 5 | Identity Platform, Maps Platform, and more |

Every service persists its state to disk (when enabled) and survives a restart.

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `KIRI_HOST` | `0.0.0.0` | Bind address |
| `KIRI_HTTP_PORT` | `4443` | REST / JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port (Pub/Sub, Firestore) |
| `KIRI_DATA_DIR` | unset | Directory for state snapshots. Enables persistence |
| `KIRI_LOG_LEVEL` | `info` | debug, info, warn, or error |

```bash
# persistent run: state survives container restarts
docker run -p 4443:4443 -p 8085:8085 \
  -e KIRI_DATA_DIR=/data -v kiri-data:/data kiri
```

## How it works

**One Go binary.** Every GCP client library accepts an endpoint override
(`option.WithEndpoint`, `client_options`, `apiEndpoint`, `setHost`, provider
`*_custom_endpoint`). Cloud Storage and Pub/Sub also honor their standard emulator
env vars. kiri answers all of it locally.

**REST plus gRPC in one process.** REST / JSON services share a single `net/http`
mux. Pub/Sub and Firestore are served over gRPC on a second port. Both sides share
the same backing state, so a Pub/Sub message published over gRPC is visible to a
REST pull.

**No protoc required.** gRPC messages are encoded with a small hand rolled
protobuf wire codec (`internal/protow`), so the build stays a plain `go build`
with no proto toolchain.

**Verified against real clients.** A proof scenario drives kiri with the
unmodified `cloud.google.com/go/storage` and `cloud.google.com/go/pubsub`
libraries end to end, including Pub/Sub streaming pull with message attributes and
a monthly cost prediction. See [`examples/scenario`](examples/scenario).

## Contributing

kiri grows by deepening service fidelity and adding coverage. A service is a
single package under `internal/service/<name>/` implementing the `Service`
interface and registering itself via `init()`. Run the suite in Docker:

```bash
docker run --rm -v "$PWD":/app -w /app golang:1.23-alpine \
  sh -c "go vet ./... && go test ./internal/..."
```

## Credits

kiri is the GCP counterpart to [`kumo`](https://github.com/sivchari/kumo), which
proved a whole cloud's SDK surface can live in one Go binary. kumo emulates AWS.
kiri brings the same drop-in local experience to Google Cloud and adds the cost
layer.

## License

[MIT](LICENSE).
