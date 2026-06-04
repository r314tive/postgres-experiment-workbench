# Release

Release artifacts are built from the Go CLI and written under ignored
`generated/release/`.

Release notes live in [../CHANGELOG.md](../CHANGELOG.md).

Build a local snapshot:

```bash
make release-snapshot
```

Override version metadata:

```bash
make release-snapshot VERSION=0.1.0
```

The snapshot target builds `pgworkbench` archives for common Linux and macOS
platforms and writes a `pgworkbench-<version>-SHA256SUMS.txt` checksum file.
Default CI keeps using source-based checks.

GitHub Actions also has a `release-snapshot` workflow. It runs on `v*` tags or
manual dispatch, builds the same archives, and uploads them as workflow
artifacts. On tag pushes it also creates or updates the matching GitHub Release
and attaches the archives plus checksum file.

## Versioning

Use `0.x` versions while the public contracts are still settling. For the first
public MVP tag, prefer `v0.1.0` after:

- local `make release-check` is green;
- GitHub `check` is green on the tag candidate commit;
- `make release-snapshot VERSION=0.1.0` builds all configured archives;
- the generated SHA256SUMS file lists every release archive;
- GitHub `release-snapshot` is green for the candidate version;
- tracked env spec docs/schema pass `make spec-docs-check`;
- [../CHANGELOG.md](../CHANGELOG.md) describes user-visible changes.

## Pre-Release Gate

Run the local release gate before tagging:

```bash
make release-check
```

The gate runs:

- `make doctor`
- `make check`
- `make quickstart`
- `make test`
- `make scan-artifacts`
- `make scan-artifacts-go`
- `make pgworkbench`
- `make privacy-scan`

When Docker daemon access is intentionally unavailable, the doctor command can
be run in no-daemon mode:

```bash
make doctor DOCTOR_FLAGS=--skip-docker-daemon
```

Use the no-daemon mode only for prerequisite triage. A release tag should still
wait for a full Docker-backed local gate or a green GitHub `check` workflow.
