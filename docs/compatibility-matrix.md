# Compatibility Support Ledger

The machine-readable support ledger lives in
[`compatibility/matrix.json`](../compatibility/matrix.json). Its schema identifier
is `pgworkbench.compatibility-matrix/v2`; its JSON Schema is
[`schemas/compatibility-matrix.schema.json`](../schemas/compatibility-matrix.schema.json).

Each cell is an exact tuple of runtime, topology, operating system, architecture,
and PostgreSQL major version. Duplicate identifiers and duplicate tuples are
invalid. The `native` runtime is currently bounded to the `single` topology.

The same ledger declares the required verification scope for every advertised
release archive platform. A `runtime-gated` platform names all exact
compatibility cells that must execute its downloaded archive before release. A
`compile-package-only` platform declares only compile, package, inventory,
checksum, reproducibility, SBOM, and provenance gates; passing those gates does
not create a runtime compatibility or support claim. Every candidate cell must
belong to exactly one runtime-gated platform, while compile/package-only
platforms must have no runtime cells. The ledger is a requirement map, not a
record that any gate has passed.

The current classification is deliberately asymmetric: `linux/amd64` and
`darwin/arm64` are runtime-gated; `darwin/amd64` and `linux/arm64` are
compile/package-only. Building four archives therefore does not imply four
runtime-qualified platforms. Runtime support for either compile/package-only
target requires a real execution cell and CI evidence for the exact downloaded
archive before changing the machine-readable classification.

## Support levels

- `candidate` means the project intends to support the cell and names the gate
  that must pass. It is not evidence that the gate has passed.
- `unsupported` is an explicit exclusion and must use the `not-applicable` gate.

The v2 ledger deliberately contains only candidate cells. A release or CI result
is separate evidence and must not rewrite a candidate declaration into a claim
of successful execution.

## Gates

- `docker-integration` requires the Docker integration suite for that exact cell.
- `native-integration` requires the native-runtime integration suite for that
  exact cell.
- `manual` reserves a bounded, documented manual verification gate.
- `not-applicable` is valid only for unsupported cells.

The package `internal/compatibility` parses JSON with unknown-field rejection,
validates the ledger, and renders deterministic JSON or Markdown. Generated
Markdown repeats the candidate assurance boundary so that a rendered table
cannot reasonably be read as a passed-test report.

## CI evidence mapping

The reusable `.github/workflows/compatibility.yml` workflow implements the
declared gates without changing the ledger's candidate status. Each Docker cell
runs one topology-specific experiment, queries every live PostgreSQL node, and
records the configured image reference, image ID, registry digest, and observed
server version. The `15->16` cell asserts both source and target majors rather
than inferring them from image tags.

Native Linux/amd64 and Darwin/arm64 jobs install PostgreSQL 16 explicitly and
verify the runtime fingerprint written by the experiment. The Darwin job also
asserts the hosted runner is actually arm64.

No Darwin/amd64 or Linux/arm64 runtime job is declared. Their release archives
remain useful build outputs, but the ledger and rendered CLI output label them
`compile-package-only` so package availability cannot become a runtime-support
inference.

Evidence artifacts use the cell ID in their names. Source-mode evidence is tied
to `github.sha`; published-mode evidence additionally starts from a downloaded,
checksum- and provenance-verified release archive. A failed, skipped, or
unavailable job leaves that cell open. No documentation or generated table
should infer a pass merely because the workflow definition exists.
