# Scenario — order ingestion pipeline

A runnable, end-to-end proof that kiri is a drop-in local GCP. It provisions and
exercises a small architecture using the **real, unmodified Google Cloud client
libraries** — not curl, not mocks — then predicts what the design would cost.

```
order file ──Storage SDK──▶ GCS bucket "orders-inbox"
order event ─Pub/Sub SDK──▶ topic "order-events" ──▶ sub "order-processor"
                            Compute Engine VM "order-worker" (n1-standard-2)
                            Cloud Billing: link, PREDICT monthly cost, budget
```

It runs six steps and asserts each one:

1. Provision object storage (real `cloud.google.com/go/storage`)
2. Provision messaging (real `cloud.google.com/go/pubsub`)
3. Provision a Compute Engine worker (REST)
4. Publish order events; the worker consumes them via streaming pull
5. Pull live SKU prices from kiri's catalog, project the monthly cost, set a budget
6. Behaviors — 404s, list scoping, instance stop/start lifecycle

## Run it

Start kiri, then point the scenario at it:

```bash
# 1. start the emulator (from the repo root)
docker build -t kiri -f docker/Dockerfile .
docker run --rm -p 4443:4443 -p 8085:8085 kiri

# 2. in another shell, run the scenario
cd examples/scenario
export GCS_ENDPOINT=http://localhost:4443/storage/v1/
export REST_ENDPOINT=http://localhost:4443
export PUBSUB_EMULATOR_HOST=localhost:8085
go run .
```

Expected tail:

```
   ✓ cost query total $69.45 matches projection $69.45
   ✓ projected spend $69.45 is within the $200 budget (34.7%)
======== SCENARIO PASSED — every layer provisioned, exercised, and billed via real clients ========
```

This module has its own `go.mod` (it depends on the real Google SDKs, which the
emulator itself does not) so it stays isolated from the main build.
