# Changelog

All notable changes to kiri are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- 108 Google Cloud services emulated from a single Go binary (REST on `:4443`,
  gRPC on `:8085`).
- Cost surface: pricing catalog, budgets, and a Cost Explorer style query
  (`/kiri/billing/cost`) for local price projection.
- Real client compatibility verified end to end via `examples/scenario` using the
  unmodified `cloud.google.com/go` libraries.
- Persistence of per-service state across restarts (`KIRI_DATA_DIR`).
- Community health files, CI (test, lint, govulncheck), and a GitHub Pages site.
