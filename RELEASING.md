# Releasing

Releases are fully automated. Nobody tags by hand and nobody edits
`CHANGELOG.md` or `version.go` manually.

## How a release happens

1. **Land a Conventional Commit on `main`.** Pull requests are squash-merged, so
   the PR title becomes the commit message. The `PR title` workflow enforces the
   format, e.g. `fix(storage): honor the Range header on objects.get`.
2. **release-please keeps a release PR open.** On every push to `main` it
   recalculates the next version from the commit history and refreshes a pull
   request titled `chore: release X.Y.Z`, which contains the version bump
   (`version.go`) and the generated `CHANGELOG.md` entry.
3. **Merge the release PR when you want to ship.** That is the only manual step.
   Merging creates the `vX.Y.Z` tag and the GitHub release.
4. **Artifacts publish automatically.** The same workflow then runs GoReleaser.

```
commit (feat:/fix:) ──► release-please PR ──► merge ──► tag vX.Y.Z
                                                          │
                                                          ▼
                                          binaries + SBOM + cosign
                                          multi-arch image → ghcr.io
                                          SLSA build provenance
```

## How the version is decided

Semantic Versioning, derived from the commit types:

| Commit                            | Bump                                     |
| --------------------------------- | ---------------------------------------- |
| `fix:`, `perf:`, `refactor:`      | patch (`0.1.0` → `0.1.1`)                |
| `feat:`                           | minor (`0.1.0` → `0.2.0`)                |
| `feat!:` / `BREAKING CHANGE:`     | minor while `0.x`, major once `1.0.0`    |
| `docs:`, `test:`, `ci:`, `chore:` | no release on their own                  |

## What each release publishes

- **Binaries** for linux, macOS, and Windows on amd64 and arm64, built with
  `-trimpath` and reproducible timestamps.
- **`*_checksums.txt`**, signed keylessly with cosign (Fulcio/Rekor).
- **SBOM** (SPDX, via syft) for every archive.
- **Multi-arch container image** at
  `ghcr.io/Brilhante29/kiri-gcp:{version}` and `:latest`, with the manifest
  signed by cosign.
- **SLSA build provenance** attested to the release archives.

## Verifying a release

Checksums:

```bash
cosign verify-blob \
  --certificate-identity-regexp '^https://github.com/Brilhante29/kiri-gcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --certificate kiri_<version>_checksums.txt.pem \
  --signature kiri_<version>_checksums.txt.sig \
  kiri_<version>_checksums.txt
```

Container image:

```bash
cosign verify ghcr.io/Brilhante29/kiri-gcp:<version> \
  --certificate-identity-regexp '^https://github.com/Brilhante29/kiri-gcp/' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Build provenance:

```bash
gh attestation verify kiri_<version>_linux_amd64.tar.gz -R Brilhante29/kiri-gcp
```

## Testing the release build locally

GoReleaser can run the whole pipeline without publishing anything:

```bash
docker run --rm -v "$PWD:/w" -w /w goreleaser/goreleaser:latest release --snapshot --clean --skip=sign,publish
```

## If a release fails midway

The tag and the GitHub release already exist at that point. Fix the problem on
`main`, then re-run the failed workflow run from the Actions tab — GoReleaser is
idempotent and will re-upload the assets for the same tag.
