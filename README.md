<div align="center">

# kiri 霧

**The cloud, brought down to your machine.**

A single-binary **Google Cloud Platform emulator** — 108 services, real SDK
compatible, offline, free. Point your client at `localhost` and build, test, and
*price* an entire GCP architecture without touching a real project.

`kumo` (雲) is the cloud, up there. **`kiri` (霧) is fog — that same cloud at
ground level, running on your laptop.**

</div>

---

## Why kiri exists

Developing against GCP puts you in a bad spot. You get two options, and both hurt:

- **Hit real GCP.** Now every test run needs credentials, a network round-trip,
  and a billing account. It's slow, it costs money, it pollutes shared
  projects, it rate-limits your CI, and it can't run on a plane or in an
  air-gapped pipeline.
- **Use the official emulators.** Google ships a handful — Pub/Sub, Firestore,
  Bigtable, Datastore, Spanner — but they're **separate binaries with separate
  flags**, they cover maybe five services, and **none of them can tell you what
  your architecture would cost.**

AWS developers solved this years ago with a single local endpoint that speaks
every service. GCP developers never got the equivalent. **kiri is that missing
piece:** one process, one endpoint, the whole platform — plus a cost surface no
other emulator has.

| | Real GCP | Official emulators | **kiri** |
|---|:---:|:---:|:---:|
| Runs offline | ✗ | ✓ | **✓** |
| Costs money | ✗ (it does) | ✓ free | **✓ free** |
| Services covered | all | ~5 | **108** |
| One binary / one endpoint | — | ✗ (5 binaries) | **✓** |
| Real client-library compatible | ✓ | ✓ | **✓** |
| **Cost / billing prediction** | ✓ | ✗ | **✓** |

---

## Quick start

### Run it (Docker)

```bash
# build the single binary image from source
docker build -t kiri -f docker/Dockerfile .

# start it — REST on 4443, gRPC on 8085
docker run --rm -p 4443:4443 -p 8085:8085 kiri
```

Health check:

```bash
curl http://localhost:4443/
# {"emulator":"kiri","status":"ok","services":108,"grpc_port":8085}
```

### Point your SDK at it

Nothing about your application code changes — only the endpoint.

**Go — Cloud Storage**

```go
client, _ := storage.NewClient(ctx,
    option.WithEndpoint("http://localhost:4443/storage/v1/"),
    option.WithoutAuthentication(),
)
```

**Go — Pub/Sub** (uses the standard emulator env var)

```bash
export PUBSUB_EMULATOR_HOST=localhost:8085
```

```go
client, _ := pubsub.NewClient(ctx, "my-project") // talks to kiri
```

**Python / Node / any REST client**

```bash
export STORAGE_EMULATOR_HOST=http://localhost:4443
export PUBSUB_EMULATOR_HOST=localhost:8085
```

That's it. Your `storage.Client`, `pubsub.Client`, and plain REST calls now hit
kiri instead of Google.

---

## The part no other emulator does: price your architecture

kiri ships a **Cost surface** — the GCP analogue of AWS Cost Explorer. It carries
a real pricing catalog (Compute, Storage, BigQuery SKUs with actual unit prices),
budgets, and a cost query grouped by service / SKU / project over a time window.

That means you can stand up an architecture locally *and predict what it would
cost in production* — before you spend a cent. Here's a real run of the included
scenario (`order ingestion pipeline`: Storage → Pub/Sub → Compute worker), driven
end-to-end by the **real Google Cloud client libraries**:

```
── STEP 5. Billing — link account, PREDICT monthly cost from live catalog
   ✓ fetched live SKU prices: vCPU=$0.031611/h RAM=$0.004237/GiB.h GCS=$0.020/GiB.mo
   predicted monthly cost:
     Compute Engine  n1-standard-2 (730h)  = $   69.35
     Cloud Storage   5 GiB-mo              = $    0.10
     ------------------------------------------
     projected total                       = $   69.45 / month
   ✓ cost query total $69.45 matches projection $69.45
   ✓ projected spend $69.45 is within the $200 budget (34.7%)
```

The projection isn't hardcoded — it reads the unit prices out of kiri's live
pricing catalog and multiplies by your provisioned resources. Change the machine
type, re-run, get a new number. Wire it into CI to fail a PR that would blow the
budget.

The full scenario source is a standalone Go program using only the real SDKs.

---

## What's covered — 108 services across 13 categories

| Category | Count | Examples |
|---|:---:|---|
| Analytics & ML | 16 | BigQuery, Dataflow, Dataproc, Vertex AI, Natural Language |
| Databases | 15 | Cloud SQL, Spanner, Bigtable, Firestore, AlloyDB, Memorystore |
| Security | 13 | IAM, Secret Manager, KMS, Binary Authorization, Cloud SCC |
| Networking | 11 | Load Balancing, Cloud DNS, Private Connect, Network Connectivity |
| Management & Billing | 8 | **Cloud Billing + Cost**, Resource Manager, Service Usage |
| Compute | 7 | Compute Engine, Cloud Run, Cloud Functions, App Engine, Batch |
| Storage | 6 | Cloud Storage, Persistent Disk, Storage Control, Archive |
| Containers | 6 | GKE, GKE Autopilot, Artifact Registry, Cloud Service Mesh |
| Application Integration | 6 | Workflows, Eventarc, API Gateway, Cloud Scheduler, Cloud Tasks |
| Messaging & Integration | 5 | Pub/Sub, Managed Kafka, FCM, Service Directory |
| Monitoring & Logging | 5 | Cloud Monitoring, Cloud Logging, Error Reporting, Cloud Trace |
| Developer Tools | 5 | Cloud Build, Cloud Deploy, Cloud Composer |
| Other Services | 5 | Identity Platform, Maps Platform, and more |

Every service persists its state to disk (when enabled) and survives a restart.

---

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `KIRI_HOST` | `0.0.0.0` | Bind address |
| `KIRI_HTTP_PORT` | `4443` | REST/JSON port |
| `KIRI_GRPC_PORT` | `8085` | gRPC port (Pub/Sub, Firestore) |
| `KIRI_DATA_DIR` | *(unset)* | Directory for state snapshots. Unset = in-memory only |

```bash
# persistent run: state survives container restarts
docker run --rm -p 4443:4443 -p 8085:8085 \
  -e KIRI_DATA_DIR=/data -v kiri-data:/data kiri
```

---

## How it works

- **One Go binary.** Every GCP client library accepts an endpoint override
  (`option.WithEndpoint`); Cloud Storage and Pub/Sub also honor their standard
  emulator env vars. kiri answers those calls locally.
- **REST + gRPC in one process.** REST/JSON services share a single `net/http`
  mux; Pub/Sub and Firestore are served over gRPC on a second port. Both sides
  share the same backing state, so a Pub/Sub message published over gRPC is
  visible to a REST pull.
- **No protoc required.** gRPC messages are encoded with a small hand-rolled
  protobuf wire codec (`internal/protow`), so the build stays a plain
  `go build` with no proto toolchain.
- **Verified against real clients.** The test suite includes a proof harness
  that drives kiri with the *unmodified* `cloud.google.com/go/storage` and
  `cloud.google.com/go/pubsub` libraries end-to-end — bucket/object round-trips
  and Pub/Sub streaming pull with message attributes — not curl mocks.

---

## Contributing

kiri grows by deepening service fidelity and adding coverage. A service is a
single package under `internal/service/<name>/` implementing the `Service`
interface and registering itself via `init()`. Run the suite in Docker:

```bash
docker run --rm -v "$PWD":/app -w /app golang:1.23-alpine \
  sh -c "go vet ./... && go test ./internal/..."
```

The route-collision check and the real-SDK proof harness should stay green on
every change.

---

## Credits

kiri is the GCP counterpart to [`kumo`](https://github.com/sivchari/kumo), which
proved a whole cloud's SDK surface can live in one Go binary. kumo emulates AWS;
kiri brings the same drop-in-local experience to Google Cloud — and adds the cost
layer.

## License

See [LICENSE](LICENSE).
