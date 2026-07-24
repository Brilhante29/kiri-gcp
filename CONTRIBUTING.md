# Contributing to kiri

Thanks for wanting to help. kiri is a community project: a single Go binary that
emulates Google Cloud locally. This guide gets you from clone to merged PR.

## Ground rules

- Be kind. See the [Code of Conduct](CODE_OF_CONDUCT.md).
- Small, focused PRs merge faster than big ones.
- Match real GCP behavior. When you implement or fix a route, follow the actual
  wire format: HTTP status codes, JSON field names, and the Google convention of
  encoding `int64`/`uint64` as JSON strings.

## Project layout

```
cmd/kiri/            entry point (main)
internal/
  server/            router, HTTP + gRPC wiring, health, config
  service/<name>/    one package per emulated service (self-registers via init())
  grpcsvc/           gRPC services (Pub/Sub, Firestore)
  protow/            hand-rolled protobuf wire codec (no protoc needed)
  httpx/             shared JSON/REST helpers
  storage/           on-disk state snapshots (KIRI_DATA_DIR)
  catalog/           service categories for docs/listing
docs/                docs and the GitHub Pages site
examples/scenario/   runnable end-to-end proof using the real Google SDKs
```

## Develop

Docker is the only requirement (no local Go toolchain needed):

```bash
# vet + test
docker run --rm -v "$PWD":/app -w /app golang:1.25-alpine \
  sh -c "go vet ./... && go test ./internal/..."

# build the image
docker build -t kiri -f docker/Dockerfile .
```

With a local Go toolchain the Makefile shortcuts work too: `make test`,
`make vet`, `make build`, `make docker`.

## Add or deepen a service

1. Create `internal/service/<name>/<name>.go`.
2. Implement the `service.Service` interface and register in `init()` with
   `service.Register(New())`.
3. Register routes in `RegisterRoutes`. Use path params (`r.PathValue(...)`),
   not hardcoded IDs.
4. Persist state through `storage.Load`/`storage.Save` so it survives a restart.
5. Give it accurate `Meta()` (Display, Category, Description).

Two rules that keep the emulator honest:

- **No route collisions.** Two services must not register the same
  `(method, path)`; the router silently drops the duplicate. When in doubt, run a
  quick scan for your new path across `internal/service`.
- **Real wire format.** If a real client library talks to your service, it must
  round-trip. The `examples/scenario` program exercises the real Google SDKs and
  is the best proof your change works end to end.

## Commit and PR

- Conventional commit subjects are appreciated (`feat:`, `fix:`, `docs:`).
- Fill in the pull request template.
- CI runs vet, tests (with `-race`), build, lint, and a vulnerability scan. Keep
  it green.

## Reporting bugs and ideas

Use the issue templates. For security problems, do not open a public issue; see
[SECURITY.md](SECURITY.md).
