# Plan: Time-Travel Billing Engine + full-service E2E harness

Goal: make kiri's cost surface *alive*. Today cost is static (someone must POST
pre-computed line items to `/kiri/billing/seed`). This plan makes costs accrue
automatically as resources exist and virtual time passes, and ties that into one
end-to-end harness that exercises every service the project ships.

Two pillars:

- **A. Time-Travel Billing Engine** — a virtual clock, a usage ledger, and a pure
  cost calculator, so you can provision, use, advance the clock 30 days, and read
  a real invoice that grew day by day.
- **B. Full E2E harness** — one runnable program plus a catalog sweep that tests
  all 108 services and drives the billing engine through simulated time.

---

## What already exists (reuse, do not rewrite)

`internal/service/billing/billing.go` (FidelityA) already has:

- Pricing catalog with real SKUs (`seedDefaults`): Compute vCPU `$0.031611/h`,
  RAM `$0.004237/GiB.h`, GCS Standard `$0.020/GiB.mo`, BigQuery `$5.0/TiB`.
- Cost query `POST /kiri/billing/cost` (Cost Explorer analogue): groups line items
  by service / sku / project / label over a `[start,end)` window.
- Manual seed `POST /kiri/billing/seed`, budgets CRUD, billing accounts, project
  linkage. Types: `costLineItem{Service,SKU,Project,Label,Cost,Currency,UsageStart,UsageEnd}`.

Confirmed integration points in the codebase:

- Hook pattern: `server.New` (around line 161) sets package vars like
  `gcspkg.PublishFunc` / `cloudschedulerpkg.PublishFunc` after the registry loop.
  This is exactly where the ledger gets injected.
- Timestamp seam: every REST service stamps `createTime`/`updateTime` via
  `httpx.Now()`. Backing that one function with the virtual clock makes every
  resource inherit virtual time with zero per-service timestamp edits.
- Flags already present: `--fidelity A,B`, `--state`, `--grpc-port 0`.
- Persistence: `storage.Load/Save(service, key, v)` gated on `KIRI_DATA_DIR`.

---

## The improvement over a naive design

A naive engine recomputes cost on every `advance` and appends the result to
`state.Costs`. That double-counts if you advance in steps or query twice, and
forces a fragile dedup between engine output and manual seeds.

This plan instead makes **cost a pure function of `(ledger, clock.now)`, computed
on read**. Consequences:

- **Idempotent**: querying twice returns the same number. Advancing 30 days in one
  jump or in 30 one-day steps yields the identical invoice.
- **No mutation on advance**: `advance` only moves the clock. Nothing to dedup.
- **Composable sources**: `queryCost` sums two independent streams: (1) manual
  seeds in `state.Costs` (keeps every existing test green) and (2) accrual
  computed live from the ledger. Both are just line items to the aggregator.

Second improvement: **daily buckets**. `queryCost` gains an optional
`granularity:"daily"` so the invoice is a curve over days, not a single total.
That is what makes "time travel" visible: the demo prints a growing bill.

---

## A. Architecture (4 new packages, ~1 wiring change)

### 1. Virtual clock — `internal/clock`

Singleton var, matching the existing `PublishFunc` global pattern. Opt-in:
untouched, it returns wall time.

```go
package clock

func Now() time.Time                 // virtual if set, else time.Now().UTC()
func Advance(d time.Duration) time.Time
func Set(t time.Time)
func Reset()                          // back to wall time
func Offset() time.Duration           // virtual - wall, for persistence
```

Single injection: change `httpx.Now()` to return `clock.Now().Format(RFC3339Nano)`
(add `internal/clock` import to `httpx`; no import cycle, clock imports nothing).
Leave `logging.go` and gRPC deadline code on wall time (never route those through
the clock, or streaming-pull timeouts break).

Admin routes, registered directly in `server.New` (outside the `--fidelity`
filter, prefix `/_kiri/` so it never collides with real GCP paths `/v1/...`):

| Method | Route | Action |
|---|---|---|
| POST | `/_kiri/time/advance?days=30` (or `?hours=`) | move the clock forward |
| GET  | `/_kiri/time/now` | `{"now":"..."}` (virtual) |
| POST | `/_kiri/time/reset` | back to wall time |

### 2. Usage ledger — `internal/billingledger`

Records the lifecycle of billable resources. Two meter kinds:

```go
type Kind int
const ( Rate Kind = iota; Counter )   // Rate = accrual (vCPU·h, GiB·mo); Counter = events (requests, ops)

type Meter struct {
    ID        string    // "compute/instances/shop-prod/us-central1-a/order-worker#vcpu"
    Service   string    // "Compute Engine"  (matches costLineItem.Service)
    SKU       string    // "N1 Predefined vCPU running" (matches a catalog SKU)
    Project   string
    Kind      Kind
    Qty       float64   // Rate: 2 vCPU / 7.5 GiB.  Counter: unused
    StartedAt time.Time // clock.Now() at create
    EndedAt   *time.Time // nil = still running
    Count     float64   // Counter: running event total
}

func Open(service, sku, project, id string, qty float64) *Meter   // Rate meter
func Meter(service, sku, project, id string)                       // Counter meter (ensure exists)
func Add(id string, n float64)                                     // Counter increment
func Close(id string)                                              // set EndedAt = clock.Now()
func Snapshot() []Meter                                            // for the engine + persistence
```

Thread-safe map keyed by ID. `nil` ledger = no-op everywhere (opt-in).

### 3. Cost engine — `internal/billingengine`

Stateless. Pure function of ledger snapshot + price lookup + window.

```go
type PriceFn func(sku string) (unitPrice float64, unit string, ok bool)

func LineItems(meters []Meter, price PriceFn, from, to time.Time) []billing.CostLineItem
func Daily(meters []Meter, price PriceFn, from, to time.Time) []DayBucket
```

Math per meter, over `[max(from,StartedAt), min(to, EndedAt|clock.now))`:

- `Rate`, unit `h`      → `qty * price * window.Hours()`
- `Rate`, unit `GiB.mo` → `qty * price * window.Hours() / 730`  (matches the catalog's month = 730h, same divisor the current scenario uses)
- `Counter`, unit `million_req` / `million_ops` → `count * price / 1e6`

Unknown SKU: skip the meter, `slog.Warn("no price for SKU", ...)`. Never fail.

### 4. Instrumentation (opt-in, ~4 lines per hook)

Each service exposes a nil-able package var (same shape as `PublishFunc`) and calls
it in its create/stop/delete handlers. `server.New` wires them after the existing
`PublishFunc` block:

| Service | Handler | Meter |
|---|---|---|
| Compute | `createInstance` | Open vCPU + RAM meters (parse `n1-standard-2` → 2 vCPU, 7.5 GiB) |
| Compute | `instanceAction stop` / `deleteInstance` | Close both meters |
| Compute | `instanceAction start` | re-Open |
| GCS | `uploadObj` | Open/adjust a `GiB.mo` meter sized `len(data)/2^30` |
| GCS | `deleteObj` | Close that object's meter |
| GCS | `getObjectMedia` | Add 1 to a GET-request Counter |
| Pub/Sub | `Publish` / ack | Add to a message-delivery Counter |
| BigQuery | query job insert | Counter on `TiB` processed (stub qty) |

New SKUs to append in `seedDefaults` (keep the catalog the single source of price):

- Pub/Sub `"Message Delivery Basic"` `$40.00/million_ops` (real-ish tier)
- GCS `"Standard Storage US GET Requests"` `$0.0004/1000_req` → model as `million_req`

### Wiring diff (one place, `server.New`)

```go
led := billingledger.New()
billingpkg.SetEngine(billingengine.New(led, billingpkg.PriceLookup()))
computepkg.Ledger = led
gcspkg.Ledger     = led
pubsubpkg.Ledger  = led
// register /_kiri/time/* admin routes on the router here too
```

### queryCost change (billing.go)

```go
items := s.state.Costs                       // existing manual seeds (tests stay green)
if s.engine != nil {                         // + live accrual from the ledger
    items = append(items, s.engine.LineItems(from, to)...)
}
// existing group-by / filter / total logic runs unchanged over the merged slice
```

Add `granularity:"daily"` branch that returns `engine.Daily(...)` buckets.

### Persistence

`billing` service already snapshots `state`. Extend its `Close()`/`New()` to also
save/load the ledger meters and `clock.Offset()`, so a restart with `KIRI_DATA_DIR`
resumes at the same virtual time with accrual intact.

---

## B. Full-service E2E harness (test everything it proposes)

Three tiers, so all 108 services get touched, not just the headline four.

**Tier 1 — real Google SDKs (FidelityA).** Extends `examples/scenario`:
GCS (bucket/object round-trip), Pub/Sub (topic/sub/publish/streaming-pull with
attributes), Firestore (doc CRUD over gRPC), Billing (link, SKU fetch).

**Tier 2 — REST domain lifecycle (FidelityB).** Compute (VM create/stop/start/list),
BigQuery (dataset/table/job), Cloud Run, Cloud Functions, GKE, IAM (SA + role),
KMS (keyring/key), Secret Manager (store/access), Resource Manager (project),
Cloud Scheduler, Cloud Tasks. Each: create → get → list → delete, assert 2xx +
well-formed JSON.

**Tier 3 — catalog sweep (the ~90 generic services).** A single Go/Python driver
reads `GET /` for the live count, then for every registered service hits its
standard `POST .../{collection}` + `GET` + `DELETE` and asserts a metadata
round-trip and persistence survival. Turns "108 services" from a claim into a
checklist.

**Cross-service:** GCS notification → Pub/Sub receive; Scheduler job → Pub/Sub.

**The master test — `examples/billing_demo`:** the demo that proves the whole
thesis. Six steps:

```
1. Provision  GCS bucket + 5 GiB objects, Compute n1-standard-2, Pub/Sub topic
2. Ledger     GET /_kiri/time/now; confirm meters open (via a debug list route)
3. Advance    POST /_kiri/time/advance?days=30
4. Invoice    POST /kiri/billing/cost {granularity:"daily"}  -> growing curve
5. Verify     total == manual projection (2*0.031611*730 + 7.5*0.004237*730 + 5*0.020)
                                          = 46.15 + 23.20 + 0.10 = $69.45 / month
6. Budget     $80 budget -> 86.8% used -> 80% threshold alert fires
```

Expected tail:

```
   ok  virtual clock advanced 30 days (2026-07-22 -> 2026-08-21)
   ok  Cost Explorer total $69.45, no manual seed:
         Compute Engine   $46.15
         Cloud Storage    $23.20
         Pub/Sub          $0.00  (3 ops, rounds to zero)
   ok  daily curve rising: day 1 $2.31 ... day 30 $69.45
   ok  budget $80 tripped 80% threshold at day 27
======== BILLING DEMO PASSED, costs accrued from real usage over virtual time ========
```

---

## Phased implementation

**Phase 1 — foundation, no SDK needed.**
`internal/clock` + tests. `internal/billingledger` + tests. `internal/billingengine`
+ tests (assert 2 vCPU * 0.031611 * 730h = $46.15, GiB.mo path = $23.20, total
$69.45; idempotent across step sizes). Register `/_kiri/time/*` in `server.New`.
Point `httpx.Now()` at `clock.Now()`.

**Phase 2 — wire into billing.** `billing.SetEngine` + `PriceLookup`. Two new SKUs
in `seedDefaults`. `queryCost` merges seeds + engine and gains `granularity:"daily"`.
Run existing `go test ./internal/...` — must stay green (opt-in proves retrocompat).

**Phase 3 — instrumentation.** Compute, GCS, Pub/Sub, BigQuery hooks (nil-safe).

**Phase 4 — harness.** `examples/billing_demo` (master test) + the Tier-3 catalog
sweep script.

**Phase 5 — tests + persistence.** `billingengine_test.go`, `time_travel_test.go`
(advance over HTTP, assert cost grows with no seed). Ledger + clock offset in the
billing snapshot. Keep `billing_test.go` untouched as the retrocompat guard.

**Phase 6 — docs.** Update the README "Price your architecture" section: the flow
is now `provision -> use -> advance -> query`, not manual seed. Bump billing
`Meta().State` to `Integrated`.

---

## Acceptance criteria

| Criterion | Check |
|---|---|
| Clock is opt-in | no `advance` call = wall time everywhere; `advance?days=30` moves virtual now |
| Accrual is lazy + idempotent | 30x `advance?days=1` == 1x `advance?days=30`; querying twice is stable |
| Engine math correct | after 730h, `POST /kiri/billing/cost` == the scenario's manual projection |
| Retrocompat | `billing_test.go`, `examples/scenario`, all `go test ./internal/...` green unchanged |
| Daily curve | `granularity:"daily"` returns a rising per-day series |
| Instrumentation | create VM/object/topic -> cost appears with no manual seed |
| Budgets | crossing a threshold after advance logs/exposes an alert |
| Persistence | with `KIRI_DATA_DIR`, virtual time + meters survive restart |
| Real clients unaffected | `cloud.google.com/go/*` round-trips unchanged; no new go.mod deps |

## Open question, answered

**Restricted route vs init flag.** Restricted route `POST /_kiri/time/advance` as
the primary control. It expresses ordering (`provision -> use -> advance -> read`)
that a static `--time-multiplier` cannot, and needs no background ticker (a ticker
would burn CPU and skew streaming-pull timeouts). A `--time-multiplier` for long
unattended demos can come later as a thin wrapper that calls `Advance` on a
goroutine; not in this plan.
