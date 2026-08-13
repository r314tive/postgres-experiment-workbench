# Evidence Format

## Experiment run contracts

Current experiment-run producer contracts are:

- `pgworkbench.run-manifest/v1` in `manifest.env`;
- `pgworkbench.run-verdict/v1` in `verdict.env` and `verdict.json`;
- `pgworkbench.run-bundle-inventory/v1` in an extracted bundle's
  `.pgworkbench-bundle.json`;
- `pgworkbench.experiment-run-result/v1` from `experiment run --json`.

The manifest records a portable spec reference, exact spec digest, a digest of
effective execution parameters (including environment overrides), resolved
experiment identity, runtime backend, optional scenario-pack identity, metrics
mode, an observed runtime fingerprint, and portable artifact root. The logical
closed-field contract is mirrored by
[`schemas/run-manifest.schema.json`](../schemas/run-manifest.schema.json). It
intentionally does not store raw hook parameters merely to make the digest
reproducible; hooks may contain private operator values.

Utility-test runs have two specs with different roles. The generated adapter
under `.tmp/utility-tests/` is recorded by `experiment_spec_*`; the reviewable
source under `utility-tests/` is recorded by the complete
`source_spec_kind`, `source_spec_id`, `source_spec_ref`, and
`source_spec_digest` tuple. Ordinary experiments serialize those four source
fields as empty strings. The verifier accepts only the all-empty form or a
complete `utility-test` tuple, checks the portable source reference against the
source ID, requires the generated reference to remain under
`.tmp/utility-tests/`, and includes all four values in the experiment identity
digest. A derived utility run cannot also claim a scenario-pack identity. This
binds both the generated adapter bytes and source utility-test bytes into the
evidence without treating the ignored generated file as a versioned
scenario-pack asset; it does not independently replay or attest the semantic
transformation between those two files.

The runner first writes `runtime_fingerprint_status=unavailable`, starts the
disposable runtime, queries the named `runtime_fingerprint_target` for
`server_version_num`, derives the PostgreSQL major, and rewrites the manifest
with status `observed`. `primary` is the target for ordinary topologies;
`multi-version-upgrade` uses `upgrade-new`, the restore destination. The OS
and architecture are the producer binary's Go execution target (`GOOS` and
`GOARCH`), not a claim about every layer of a container image. A failed run may
remain `unavailable` when the server never became queryable; a passed v1 run
must be `observed`.

The singular PostgreSQL version in the manifest describes only that named
target. In particular, it is not evidence of the source/target version pair in
a multi-version topology. The upgrade workload and assertions observe both
ends, while qualification of an exact `old->new` compatibility cell still
requires separate gate evidence for the pair.

The verdict binds to the exact bytes of `manifest.env`, repeats the experiment
identity, records terminal phase exits, and uses `run_dir=.`. An early failure
after manifest creation must still produce a failed verdict. Status is a closed
`passed|failed` enum: `passed` requires all three recorded exits to be zero,
while `failed` requires at least one nonzero exit. Both the writer and verifier
enforce this independently for env and JSON artifacts. Background workloads and
the metrics sampler contribute their real child exit status; before returning a
successful command, the runner also verifies the completed live artifact and
rewrites the terminal verdict to failed if that check does not pass.

When a spec declares a fixed `EXPERIMENT_METRICS_SAMPLES` count, the versioned
manifest records it and verification requires `metrics.csv` to contain exactly
that many data rows for a passed run. An empty value means duration-based
sampling and still requires at least one sample for a passed metrics-enabled
run. A terminal failed verdict may omit `metrics.csv` when setup ended before
the sampler could start; if the file exists it is still validated, and its
absence is never converted into a synthetic observation.

Bundle creation sorts files, normalizes tar/gzip metadata, hashes every source
file while copying, adds a complete content inventory, and returns an archive
digest. Verification after extraction checks schemas, cross-file bindings,
optional metrics semantics, and the complete inventory without relying on the
producer's old absolute path. Required manifest/verdict files, optional metrics,
and the bundle inventory must be regular files; symlinks are rejected before
their content is read.

Use `pgworkbench run verify --bundle <extracted-run-dir>` for an extracted
bundle. In this mode `.pgworkbench-bundle.json` is mandatory, and every regular
file other than the inventory itself must have exactly one matching size and
SHA-256 entry. A missing inventory, a missing or changed inventoried artifact,
or an unlisted artifact fails verification. Plain `run verify` deliberately
keeps inventory optional for a live run directory that has not been bundled;
it must not be used to claim verification of an extracted complete bundle.
For utility-derived bundles the verifier also requires captured generated and
source spec bytes and compares both to their manifest digests. Declared utility
outputs must each exist as a non-empty regular file below `artifacts/utility/`
and are covered by the same inventory.

The schemas under `schemas/` describe JSON artifacts. The env manifest/verdict
use the same closed field set enforced directly by `pgworkbench run verify`.
Legacy unversioned runs remain readable, but new producers emit only v1.

Runtime fingerprint verification is deliberately bounded. It checks canonical
fields, derives the major again from `server_version_num`, includes the values
in experiment identity, and binds the final manifest to the verdict. It does
not independently attest the producer host, authenticate the PostgreSQL server,
or turn one observed tuple into a compatibility or support claim.

## Benchmark contracts

The implemented benchmark JSON-schema contracts are listed below. Some are
complete files and some are typed subdocuments embedded in another artifact;
the table makes that storage boundary explicit.

| Contract | Schema | Producer location |
| --- | --- | --- |
| `pgworkbench.benchmark-protocol/v1` | [`benchmark-plan.schema.json`](../schemas/benchmark-plan.schema.json) | `plan.json`; its version field is `protocol_schema_version` |
| `pgworkbench.benchmark-protocol/v2` | [`benchmark-plan.schema.json`](../schemas/benchmark-plan.schema.json) | explicitly opted-in `plan.json`; never inferred from a v1 spec |
| `pgworkbench.benchmark-cache-state/v1` | [`benchmark-cache-state.schema.json`](../schemas/benchmark-cache-state.schema.json) | linked trial `artifacts/benchmark/controls/cache-state.json`, re-derived from sibling `cache-state.tsv` |
| `pgworkbench.benchmark-statistics-reset/v1` | [`benchmark-statistics-reset.schema.json`](../schemas/benchmark-statistics-reset.schema.json) | linked trial `artifacts/benchmark/controls/statistics-reset.json`, re-derived from sibling `statistics-reset.tsv` |
| `pgworkbench.benchmark-collector-overhead/v1` | [`benchmark-collector-overhead.schema.json`](../schemas/benchmark-collector-overhead.schema.json) | linked trial `artifacts/benchmark/controls/collector-overhead.json`, re-derived from sibling `collector-overhead.tsv` |
| `pgworkbench.benchmark-resource-budget/v1` | [`benchmark-resource-budget.schema.json`](../schemas/benchmark-resource-budget.schema.json) | linked trial `artifacts/benchmark/controls/resource-budget.json`, re-derived from sibling `resource-budget-source.json` |
| `pgworkbench.pgbench-result/v1` | [`benchmark-pgbench-result.schema.json`](../schemas/benchmark-pgbench-result.schema.json) | `trials/NNN.json` under `pgbench`; parsed from the linked run's final summary |
| `pgworkbench.pgbench-log-result/v1` | [`benchmark-pgbench-log-result.schema.json`](../schemas/benchmark-pgbench-log-result.schema.json) | `trials/NNN.json` under `transaction_log`; parsed from all linked plain worker logs |
| `pgworkbench.benchmark-postgres-metrics/v2` | [`benchmark-postgres-metrics.schema.json`](../schemas/benchmark-postgres-metrics.schema.json) | mandatory `trials/NNN.json` subdocument for a passed trial; independently derived from linked `metrics.csv`, the declared collector interval, and protocol-v2 reset/timing controls over the measure window |
| `pgworkbench.benchmark-phase-timeline/v3` | [`benchmark-phase-timeline.schema.json`](../schemas/benchmark-phase-timeline.schema.json) | mandatory in new `trials/NNN.json`; normalized from the linked run's eleven-row `artifacts/benchmark/phases.tsv`, whose run/trial-bound rows must byte-match `driver-logs/trial-NNN-phases.tsv`; the parser retains read/verify compatibility for legacy v1/v2 timelines |
| `pgworkbench.benchmark-effective-settings/v1` | [`benchmark-effective-settings.schema.json`](../schemas/benchmark-effective-settings.schema.json) | A/B-only `trials/NNN.json` subdocument; independently re-derived from the linked run's narrow `artifacts/benchmark/effective-pg-settings.tsv` inside the prepare phase |
| `pgworkbench.benchmark-trial/v1` | [`benchmark-trial.schema.json`](../schemas/benchmark-trial.schema.json) | `trials/NNN.json` |
| `pgworkbench.benchmark-environment/v1` | [`benchmark-environment.schema.json`](../schemas/benchmark-environment.schema.json) | `environment.json` after at least one valid trial establishes the series environment |
| `pgworkbench.benchmark-scenario-pack/v1` | [`benchmark-scenario-pack.schema.json`](../schemas/benchmark-scenario-pack.schema.json) | pack-bound series `protocol/scenario-pack.json`; portable full file inventory whose exact bytes and independently recomputed pack digest are bound from `result.json` |
| `pgworkbench.native-toolchain/v1` | [`native-toolchain.schema.json`](../schemas/native-toolchain.schema.json) | native ordinary-benchmark and operation series `protocol/native-toolchain/manifest.json`, plus A/B v3 `toolchains/<role>/manifest.json`; an A/B-linked ordinary series keeps its own canonical series-local snapshot in addition to the arm-level binding. Each manifest binds an identity-only snapshot of seven native PostgreSQL executables and their observed versions; adjacent installation/system dependencies and source/build provenance remain unattested |
| `pgworkbench.benchmark-series/v1` | [`benchmark-series.schema.json`](../schemas/benchmark-series.schema.json) | `result.json` |
| `pgworkbench.benchmark-comparison/v1` | [`benchmark-comparison.schema.json`](../schemas/benchmark-comparison.schema.json) | `benchmark compare --json` output; not persisted automatically |
| `pgworkbench.benchmark-bundle/v1` | [`benchmark-bundle-inventory.schema.json`](../schemas/benchmark-bundle-inventory.schema.json) | `benchmark-bundle.json` at the extracted bundle root |
| `pgworkbench.benchmark-host-qualification/v1` | [`benchmark-host-qualification.schema.json`](../schemas/benchmark-host-qualification.schema.json) | standalone `benchmark host-inspect --output` artifact; unsigned and operator-recorded |
| `pgworkbench.benchmark-ab-protocol/v3` | [`benchmark-ab-protocol.schema.json`](../schemas/benchmark-ab-protocol.schema.json) | `runs/benchmark-ab/<id>/protocol.json`; binds `pg_config` or the bounded native executable-byte-set subject plus the assigned-GUC parser/source contract |
| `pgworkbench.benchmark-ab-run/v3` | [`benchmark-ab-run.schema.json`](../schemas/benchmark-ab-run.schema.json) | `runs/benchmark-ab/<id>/result.json`; includes independently derived per-arm effective-settings assessment |
| `pgworkbench.benchmark-ab-bundle/v1` | [`benchmark-ab-bundle-inventory.schema.json`](../schemas/benchmark-ab-bundle-inventory.schema.json) | `benchmark-ab-bundle.json` at the extracted A/B bundle root |
| `pgworkbench.benchmark-history/v1` | [`benchmark-history.schema.json`](../schemas/benchmark-history.schema.json) | `runs/benchmark-history/<id>/result.json`; compatible series over time in one canonical environment-population digest, normalizing only the native snapshot's run-specific location, always descriptive |
| `pgworkbench.benchmark-history-bundle/v1` | [`benchmark-history-bundle-inventory.schema.json`](../schemas/benchmark-history-bundle-inventory.schema.json) | `benchmark-history-bundle.json` at the extracted history bundle root |
| `pgworkbench.benchmark-campaign-protocol/v1` | [`benchmark-campaign-protocol.schema.json`](../schemas/benchmark-campaign-protocol.schema.json) | `runs/benchmark-campaign/<id>/protocol.json`; predeclared ordered independent series |
| `pgworkbench.benchmark-campaign-execution/v1` | [`benchmark-campaign-execution.schema.json`](../schemas/benchmark-campaign-execution.schema.json) | `runs/benchmark-campaign/<id>/executions/NNN.json` |
| `pgworkbench.benchmark-campaign-run/v1` | [`benchmark-campaign-run.schema.json`](../schemas/benchmark-campaign-run.schema.json) | `runs/benchmark-campaign/<id>/result.json`; always descriptive with no aggregate decision |
| `pgworkbench.benchmark-campaign-bundle/v1` | [`benchmark-campaign-bundle-inventory.schema.json`](../schemas/benchmark-campaign-bundle-inventory.schema.json) | `benchmark-campaign-bundle.json` at the extracted campaign bundle root |
| `pgworkbench.operation-benchmark-spec/v1` | [`operation-benchmark-spec.schema.json`](../schemas/operation-benchmark-spec.schema.json) | strict source `benchmarks/operations/**/*.json`, retained as an immutable series snapshot |
| `pgworkbench.operation-result/v1` | [`operation-result.schema.json`](../schemas/operation-result.schema.json) | exact standardized linked-run result for `operation-result` measurement bases; never interpreted as TPS |
| `pgworkbench.operation-benchmark-series/v1` | [`operation-benchmark-series.schema.json`](../schemas/operation-benchmark-series.schema.json) | descriptive `runs/operation-benchmarks/<id>/result.json`; binds the clean execution contract, bounded recomputable input closure, retained engine bytes, and native seven-file toolchain identity when applicable |
| `pgworkbench.operation-benchmark-bundle/v1` | [`operation-benchmark-bundle-inventory.schema.json`](../schemas/operation-benchmark-bundle-inventory.schema.json) | unsigned `operation-benchmark-bundle.json` at the extracted bundle root; its canonical `series_ref` is required to resolve to the exact series being verified |
| `pgworkbench.benchmark-import/v1` contract `1.1.0` | [`benchmark-import.schema.json`](../schemas/benchmark-import.schema.json) | descriptive offline import `result.json`; strict pinned parsers record driver commit and error/timing completeness without becoming series or decision input |
| `pgworkbench.benchmark-import-mapping/v1` | [`benchmark-import-mapping.schema.json`](../schemas/benchmark-import-mapping.schema.json) | retained `raw/mapping.json` only for generic legacy HammerDB/BenchBase typed JSON Pointer selection; pinned strict formats need no mapping |
| `pgworkbench.benchmark-driver-execution/v2` contract `2.0.0` | [`benchmark-driver-execution.schema.json`](../schemas/benchmark-driver-execution.schema.json) | immutable descriptive native BenchBase/HammerDB/sysbench single-trial envelope; binds fixed argv, loopback/non-system target policy plus explicit operator acknowledgement, adapter-selected staged runtime closure, retained lock/input/output bytes, and nested strict import without ownership, complete host-runtime, source-to-binary, or decision claims |
| `pgworkbench.benchmark-driver-execution-inventory/v2` | [`benchmark-driver-execution-inventory.schema.json`](../schemas/benchmark-driver-execution-inventory.schema.json) | closed exact file set for a relocatable external-driver execution directory, including every retained runtime-closure file |
| `pgworkbench.sysbench-native-run-config/v1` | [`benchmark-driver-sysbench-config.schema.json`](../schemas/benchmark-driver-sysbench-config.schema.json) | closed non-secret sysbench PostgreSQL target and workload-control input used to reconstruct fixed argv |
| `pgworkbench.hammerdb-v6-native-run-config/v1` | [`benchmark-driver-hammerdb-config.schema.json`](../schemas/benchmark-driver-hammerdb-config.schema.json) | closed non-secret HammerDB v6.0 PostgreSQL TPROC-C/TPROC-H execute-only input used to reconstruct adapter-generated Tcl |
| `pgworkbench.benchmark-import-bundle/v1` | [`benchmark-import-bundle-inventory.schema.json`](../schemas/benchmark-import-bundle-inventory.schema.json) | `benchmark-import-bundle.json` at the extracted import bundle root |

External-driver execution v2 adds
`execution.json.inputs.driver_runtime`. It records the adapter strategy,
canonical `inputs/runtime` root, retained entrypoint, sorted file references
with normalized modes, file count, total bytes, and a SHA-256 digest of the
canonical runtime-file list. The execution inventory separately closes the
whole artifact directory. Verification scans both: an unlisted artifact file,
a missing or extra runtime file, mode drift, a changed byte, or a runtime tree
that cannot be independently reconstructed for the pinned adapter fails.
The invocation records `adapter-owned-staged-driver-runtime-v2`,
`staged-copy-with-pre-post-tree-match`, and `minimal-fixed-v2`; a verifier does
not accept the legacy invocation policy under this schema.

The three runtime strategies have intentionally different closures. BenchBase
uses `jar-manifest-transitive-closure/v1`, follows the entry JAR's transitive
manifest `Class-Path`, and adds `config/plugin.xml`; HammerDB uses
`hammerdb-self-contained-launcher/v1` and requires exactly the staged `hammerdbcli`
file; sysbench uses `sysbench-pinned-lua-closure/v1` and requires the staged
executable, selected workload Lua file, and `oltp_common.lua`, with a fixed
staged `LUA_PATH`. The corresponding entrypoints are
`inputs/runtime/benchbase.jar`, `inputs/runtime/hammerdbcli`, and
`inputs/runtime/bin/sysbench`. These controls justify only
`driver_runtime_closure_attested=true`. The same artifact requires
`source_to_binary_attested=false` and
`host_runtime_dependencies_attested=false`: the registry source relation, JRE,
dynamic libraries, kernel, and database preparation are not proven by the
runtime tree.

Series verification parses the immutable `benchmark-spec.env` snapshot and
checks the normalized target/endpoint contract, target-topology source,
cache/reset/collector, placement, resource-budget, and random-seed declarations
against `plan.json`. Replacing a valid plan value and
recomputing its identity digests therefore does not detach it from its source
spec snapshot.

The final summary and transaction-log normalizations are deliberately separate.
The former records pgbench's run-level totals and means; the latter derives
per-transaction latency distributions and counter evidence. The raw files stay
unchanged in the linked experiment run and are referenced from each trial by
portable path, size, and SHA-256 digest. For ordinary closed-loop output,
pgbench's summary mean is derived from its global client-time window rather
than the plain log's transaction-interval accumulator; verification retains
both and permits only the documented bounded gap.

The PostgreSQL sampler normalization selects the minimal sample sequence that
brackets the measure phase. It records exact CSV identity and coverage, the
declared interval, the observed maximum consecutive gap, an explicit maximum
gap of two declared intervals, lossless decimal deltas for
`pg_stat_database`/`pg_stat_wal` cumulative counters, and mean/max values for
session/lock gauges. Protocol v2 additionally binds the typed collector/reset
artifacts and their raw sources. Calibrated timing rows must correlate one for
one with regular `metrics.csv` rows; at most one intentionally untimed terminal
boundary row is allowed.

A cumulative counter decrease is invalid unless the matching satisfied
runner-managed reset timestamp lies between exactly those two selected
samples. Across that proven boundary, normalization sums the observed
pre-reset increments and the current post-reset cumulative value and marks a
two-segment delta. This is an observed lower bound: work between the last
pre-reset sample and the reset itself is not recoverable. A second or
wrong-scope decrease, a cadence gap over the explicit bound, changed database,
malformed/non-monotonic timestamp, missing boundary sample, or unsupported
PostgreSQL 15-19 major makes the trial invalid. Verification reopens
`metrics.csv` plus both control JSON/raw pairs and re-derives the complete
subdocument; the deltas remain descriptive observations, not tuning or causal
conclusions.

### CLI lifecycle

```bash
# Resolve the versioned protocol without executing it.
pgworkbench benchmark plan --json pgbench/smoke

# Produce a series and its linked experiment runs.
pgworkbench benchmark run \
  --runtime docker --run-id example-series --subject baseline pgbench/smoke

# Read and then independently verify the live series artifact.
pgworkbench benchmark run-show --json example-series
pgworkbench benchmark run-verify --json example-series

# Verification is a precondition of bundle creation.
pgworkbench benchmark run-bundle --json \
  example-series generated/example-series.tar.gz

# After extraction, point --bundle at the series directory inside the archive.
pgworkbench benchmark run-verify --json --bundle \
  <extracted-root>/runs/benchmarks/example-series

# Verify/compare two distinct series and emit, but do not auto-save, a result.
pgworkbench benchmark compare --json <baseline-series> <candidate-series>

# Record and independently verify a bounded operator-recorded host snapshot.
pgworkbench benchmark host-inspect --output host-qualification.json \
  --storage-path /path/to/postgres-data --storage-label postgres-data \
  --client-placement same-host
pgworkbench benchmark host-verify --json host-qualification.json

# Execute two fresh series in a predeclared AB/BA schedule. The complete strict
# qualification and analysis options are listed in benchmark-ab.md.
pgworkbench benchmark ab-run [options] \
  <baseline-benchmark> <candidate-benchmark>
pgworkbench benchmark ab-show --json <ab-run-id>
pgworkbench benchmark ab-verify --json <ab-run-id>
pgworkbench benchmark ab-bundle \
  <ab-run-id> generated/benchmark-ab.tar.gz
pgworkbench benchmark ab-verify --json --bundle \
  <extracted-root>/runs/benchmark-ab/<ab-run-id>

# Compatible series over time remain a descriptive history.
pgworkbench benchmark history-create --history-id nightly \
  <series-a> <series-b>
pgworkbench benchmark history-verify nightly
pgworkbench benchmark history-bundle nightly generated/nightly.tar.gz

# Heterogeneous specs may share a predeclared descriptive campaign.
pgworkbench benchmark campaign-run --campaign-id saturation \
  pgbench/saturation/c01 pgbench/saturation/c04
pgworkbench benchmark campaign-verify saturation
pgworkbench benchmark campaign-bundle saturation generated/saturation.tar.gz

# A descriptive external-driver import has its own relocated inventory gate.
pgworkbench benchmark import-bundle <import> generated/import.tar.gz
pgworkbench benchmark import-verify --bundle \
  <extracted-root>/imports/<artifact-digest>

# A native external-driver execution includes its closed staged runtime closure
# and is already a closed relocatable directory.
pgworkbench benchmark driver-run-verify <driver-execution>/execution.json
```

`benchmark run` internally verifies every successful linked experiment before
accepting its normalized trial. It does not replace the series-level
`benchmark run-verify` command. Plain verification checks a live series and its
linked runs but does not require a bundle inventory. Bundle verification
requires the inventory and rejects missing, changed, duplicate, or unlisted
regular files across the complete extracted tree.

Current comparison output is also bounded evidence. Every producer-generated
environment has `qualification=unqualified-local`; smoke series use evidence
class `unqualified-local-smoke` and measurement series use
`unqualified-local-measurement`. More fundamentally, independent series are not
a counterbalanced schedule. Therefore ordinary comparison artifacts may report
invalid, not-comparable, or inconclusive input, but can never issue an improved,
regressed, equivalence, or no-regression performance verdict, even if an
operator edits a qualification label.

### Live artifact tree

A completed live series has this shape. Parenthesized entries are conditional;
a failed execution may stop before every planned trial is produced.

```text
runs/benchmarks/<series-id>/
├── benchmark-spec.env
├── plan.json
├── protocol/
│   ├── scenario-pack.json             (full portable inventory when pack-bound)
│   ├── experiment-spec.env
│   ├── workload-spec.env
│   ├── postgresql.conf
│   ├── target-topology.env
│   └── workload-script.sql          (custom workload only)
├── environment.json                 (after a valid trial)
├── result.json
├── runs.tsv
├── summary.md
├── trials/
│   ├── 001.json
│   └── ...
└── driver-logs/
    ├── trial-001.log
    ├── trial-001-phases.tsv
    └── ...

runs/<series-id>-t001/
├── manifest.env
├── verdict.env
├── verdict.json
├── driver/
│   ├── pgbench-summary.log
│   └── pgbench-raw/
│       └── <plain worker logs>
├── artifacts/benchmark/
│   ├── phases.tsv                     (primary run/trial-bound journal)
│   ├── effective-pg-settings.tsv      (counterbalanced A/B trials only)
│   └── controls/                      (protocol v2 only)
│       ├── cache-state.{json,tsv}
│       ├── statistics-reset.{json,tsv}
│       ├── collector-overhead.{json,tsv}
│       ├── resource-budget.json
│       └── resource-budget-source.json
├── metrics.csv                       (when enabled)
├── snapshots/                        (when enabled)
└── <other normal experiment evidence>
```

`plan.json` uses portable scenario-pack references, and the protocol inputs are
copied below the series directory; neither requires the producer's old absolute
source paths. `result.json` is the authoritative series document. `runs.tsv`
and `summary.md` are human-oriented indexes, while each trial JSON binds the
linked experiment run, final summary, raw transaction logs, normalization
result, normalized phase timeline, validity status/reasons, primary metric, and
the bounded environment digest when that trial established one. A/B trials also
bind a normalized `effective_settings` record to the exact raw TSV, A/B
protocol digest, trial identity, server version, and prepare-phase journal.

When scenario-pack identity is configured, `result.json.scenario_pack` binds the
pack id/version/content digest, the canonical inventory reference, and the exact
inventory-file digest. Verification strictly reparses the retained manifest and
sorted file list and independently recomputes the pack digest. Producer-side
full-pack checks occur immediately before and after each trial and before series
finalization. Because execution still uses the live pack root, a transient
change-and-revert wholly inside a trial remains outside this evidence claim.

### Bundle tree

Bundle creation first verifies the live series, copies the complete series tree
and every distinct linked experiment run, inventories the copied regular files,
and creates a deterministic archive. After extraction its layout is:

```text
pgworkbench-benchmark-<series-id>/
├── benchmark-bundle.json
└── runs/
    ├── benchmarks/<series-id>/...
    ├── <series-id>-t001/...
    └── ...
```

The inventory identifies the series and binds every other regular file by
portable path, size, and SHA-256 digest. Symlinks and non-regular source files
are rejected. Relocated verification infers the artifact root from the supplied
series directory and never relies on the producer's old absolute path.

### Bounded environment fingerprint

`environment.json` binds the runtime kind, producer `GOOS`/`GOARCH`, driver and
parser versions, observed PostgreSQL server version/major, PostgreSQL
configuration identity, engine version/commit and retained executable digest,
scenario-pack identity, and the fixed `unqualified-local` qualification into a
digest shared by every valid trial in the series. Docker rows also include the
local driver/target image IDs reported by Compose; native rows bind the seven-file
PostgreSQL toolchain snapshot. Environment drift inside one series makes the
affected trial invalid.

This is a **bounded runtime fingerprint**, not an exact environment capture. It
does not attest hardware topology, kernel policy, image publisher/signature or
repository digest, firmware, storage path, declared client placement, clocks, thermal state, or
background interference. `plan.json` identity-binds cache/reset/collector,
client-placement, and resource-budget declarations, but declaration is not
attestation or enforcement. A separate host-qualification artifact records a selected subset
of those observations, but remains unsigned and operator-recorded: verification
does not establish host identity, dedicated ownership, current state, or
collection provenance. The v1 PostgreSQL sampler identity and interval are
bound and its measurement-window coverage is verified. Metrics v2 also records
and enforces the explicit two-interval wall-clock cadence bound; v1 declarations for
cache state, operator-managed resets, overhead calibration, and resource
budgets are not independently proved by an ordinary series. Explicit protocol
v2 instead requires the four run/trial/protocol-bound control artifacts above.
Each is strictly parsed, bound to a phase window, checked against its raw digest
and size, and independently re-derived. Calibrated overhead rows must follow
the exact nominal interval; metrics normalization permits only one separately
identified untimed terminal boundary sample. These checks
remain unsigned local evidence, and the cache artifact covers PostgreSQL
shared-buffer residency rather than the OS page cache.

### Counterbalanced A/B closure

The candidate implements the A/B producer and independent live/bundle verifier.
Its orchestration directory is `runs/benchmark-ab/<id>/`, while the
baseline and candidate stay as ordinary immutable series under
`runs/benchmarks/<id>-a/` and `runs/benchmarks/<id>-b/`. `result.json` uses
artifact-root-relative references for both series and A/B-run-relative file
references for the protocol and before/after qualification records. The
verifier reopens both complete series and bookends, reconstructs every block,
reruns paired analysis, re-parses every narrow effective `pg_settings` source,
requires within-arm stability and a real cross-arm value/unit difference, and
re-derives the terminal decision. `ab-bundle`
captures that complete closure plus every linked experiment run in a sorted
digest/size inventory; `ab-verify --bundle` requires and checks that inventory
after relocation.

```text
pgworkbench-benchmark-ab-<id>/
├── benchmark-ab-bundle.json
└── runs/
    ├── benchmark-ab/<id>/...
    ├── benchmarks/<id>-a/...
    ├── benchmarks/<id>-b/...
    └── <all distinct linked experiment runs>/...
```

The inventory excludes itself and binds every other regular file by portable
path, size, and SHA-256 digest. Bundle creation rejects symlink or non-regular
sources; required-inventory verification rejects missing, changed, duplicate,
or unlisted files.
The full scheduling, population, bookend, statistical, and assurance contract
is specified in [benchmark-ab.md](benchmark-ab.md).
