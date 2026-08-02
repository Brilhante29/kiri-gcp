# Changelog

All notable changes to kiri are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0](https://github.com/Brilhante29/kiri-gcp/compare/v0.1.0...v0.2.0) (2026-08-02)


### Features

* kiri — single-binary Google Cloud emulator ([1769957](https://github.com/Brilhante29/kiri-gcp/commit/1769957ebcc9e8de214e66d25da67319e577893a))
* rebrand to kiri, add multi-language examples, refresh docs and harden gitignore ([2ab3c5d](https://github.com/Brilhante29/kiri-gcp/commit/2ab3c5d497df6eba865f6441774c6f3af146fcf8))


### Bug fixes

* **security:** pin Docker base images to immutable digests ([18c95c6](https://github.com/Brilhante29/kiri-gcp/commit/18c95c6fdbcba13fb7419904232ef6ca5a1ce8d8))


### Documentation

* multi-client docs, minimalist page, drop em-dashes ([0c886c2](https://github.com/Brilhante29/kiri-gcp/commit/0c886c28ad3699f02791b02d452f18d3d1c4e9e6))

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
