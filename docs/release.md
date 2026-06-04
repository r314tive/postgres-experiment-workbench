# Release

Release artifacts are built from the Go CLI and written under ignored
`generated/release/`.

Build a local snapshot:

```bash
make release-snapshot
```

Override version metadata:

```bash
make release-snapshot VERSION=0.1.0
```

The snapshot target builds `pgworkbench` archives for common Linux and macOS
platforms. Default CI keeps using source-based checks; tagged release publishing
can be added once the MVP contract is stable.

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
