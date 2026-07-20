# Release

Release artifacts are built from the Go CLI and written under ignored
`generated/release/`. Release notes live in [../CHANGELOG.md](../CHANGELOG.md),
and the active milestone is described in [roadmap.md](roadmap.md).

## v0.1.37 candidate

The massive-DML consolidation is ready for a candidate commit only after this
local sequence succeeds:

```bash
make check
make test

MATRIX_PROFILE_SIZES=medium MATRIX_REPEATS=3 \
  make matrix-run MATRIX_SPEC=massive-dml-strategy

make release-check
make release-snapshot VERSION=0.1.37
```

Verify every matrix row and release archive:

```bash
make experiment-summary SUMMARY_INPUT=runs/matrices/<matrix-run-id>
make scan-artifacts
make privacy-scan
cd generated/release && shasum -a 256 -c pgworkbench-0.1.37-SHA256SUMS.txt
```

The release snapshot target builds `pgworkbench` archives for supported Linux
and macOS platforms and writes a checksum file listing every archive.

## Candidate to release

1. Commit and push the candidate changes.
2. Require the GitHub `check` workflow to pass on that exact commit.
3. Add a dated `v0.1.37` changelog heading and tag the exact green commit.
4. Push the tag and require the GitHub `release-snapshot` workflow to pass.
5. Verify that the GitHub Release contains every archive and its checksum file.
6. Pin external documentation to the tag before redirecting or archiving the
   standalone massive-DML repository.

Do not tag from an uncommitted worktree, and do not archive the standalone lab
before the pinned workbench release is reachable.

## What `make release-check` covers

- `make doctor`
- `make check`
- `make quickstart`
- `make test`
- `make scan-artifacts`
- `make scan-artifacts-go`
- `make pgworkbench`
- `make privacy-scan`

When Docker daemon access is intentionally unavailable, use
`make doctor DOCTOR_FLAGS=--skip-docker-daemon` only for prerequisite triage. A
tag still waits for a full Docker-backed local gate or a green GitHub `check`.

## Versioning

Use `0.x` versions while public contracts are settling. Every version bump must
have a changelog entry, green spec-doc drift checks, a successful local release
snapshot, complete checksums, and green tag workflows.
