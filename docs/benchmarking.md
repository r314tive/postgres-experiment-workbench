# Benchmarking

PostgreSQL Experiment Workbench is intended to become a reproducible
PostgreSQL performance-regression laboratory. It owns experiment identity,
isolation, protocol, evidence, and bounded decisions; it does not reimplement
established load generators.

The adapter order is deliberate. Execution and offline import are separate
capabilities:

1. `pgbench` is the gold/reference adapter and defines the driver contract.
2. BenchBase `33c0047` has a bounded native execution envelope for broader
   workload families.
3. HammerDB v6.0 has a bounded execute-only native envelope for PostgreSQL
   TPROC-C/TPROC-H plus a strict saved-job-report importer.
4. sysbench 1.0.20 has a bounded native execution envelope for retained Lua
   workloads and strict console-summary normalization.

This project does not claim TPC compliance, publish a cross-system leaderboard,
or collapse unrelated measurements into a composite score. It answers bounded
questions such as “did this PostgreSQL change cause a practically important
regression under this recorded protocol?” only after the required evidence gate
exists and passes.

## Current implementation boundary

The repository currently implements a **local bounded benchmark system**.
Ordinary series remain unqualified; only the separate A/B path can apply
recorded qualification bookends. The distinction below is part of the contract.

| Capability | Current state |
| --- | --- |
| Execution | `pgbench` against the owned direct PostgreSQL endpoint on Docker/native, or the owned PgBouncer endpoint on the Docker `pgbouncer` topology; descriptive native single-trial envelopes for pinned BenchBase `33c0047`, HammerDB `v6.0`, and sysbench `1.0.20` driver records |
| Offline import | Pinned BenchBase `33c0047` summaries and HammerDB v6.0 saved job reports through strict shape parsers; sysbench 1.0 through a strict console-summary parser; generic HammerDB/BenchBase JSON remains available through explicit typed JSON Pointer mappings |
| Protocol | Versioned plan and retained typed protocol capsule; complete configured-pack inventory plus pre/post-trial and finalization revalidation; an eleven-phase per-trial timeline with isolated control windows |
| Raw evidence | Final pgbench summary, plain per-transaction logs, the linked experiment artifact, and a per-trial driver transcript |
| Normalization | Strict final-summary and transaction-log parsing plus cadence-bounded, measure-scoped PostgreSQL counter deltas and session/lock gauges re-derived from raw `metrics.csv`; protocol v2 also reopens typed reset/timing controls |
| Repetition | Independent sequential trials, retained individually, with a declared minimum-valid count and maximum CV |
| Statistics | Mean, median, sample standard deviation, CV, MAD, robust CV, minimum, and maximum; no automatic outlier removal |
| Portability | Verifiable series, histories, campaigns, imports, and counterbalanced A/B artifacts, with deterministic inventory-bound bundles containing their complete evidence closure |
| Qualification | A standalone strict host snapshot/verification contract exists, but it is unsigned, operator-recorded, and cannot elevate an ordinary series; benchmark environments remain `unqualified-local` |
| Comparison | Ordinary independent-series comparison is permanently descriptive; the separate counterbalanced AB/BA producer and verifier may decide only after all execution, population, policy, and bookend gates pass |
| Operation benchmarks | A separate descriptive contract repeats exact experiment runs for non-TPS metrics, strictly parses standardized result JSON (or a declared linked-run wall clock), recomputes statistics, and bundles complete linked-run evidence; it is permanently decision-ineligible |

Ordinary `benchmark run` records a first-class eleven-phase timeline but does not
itself become paired evidence. The separate `benchmark ab-run` producer
predeclares and executes alternating AB/BA blocks, records before/after host
qualification, and binds two fresh role-specific series. It still does not bind
versioned host performance time series. The plan binds target name,
endpoint-contract version, target topology and topology-spec digest as well as
the collector set and interval selected by its explicit protocol contract: v1 uses
`pgbench-driver` plus `postgresql-sampler-v1`, while v2 uses
`pgbench-driver` plus `postgresql-sampler-v2` and the controls described below.
Standalone host inspection remains a bounded recorded observation, not proof
of a dedicated host.

The `--subject` option records a label. It does not apply a configuration,
checkout, binary, or database change. A real subject difference must be
introduced through a reviewed protocol input and permitted by the comparison
identity. Counterbalanced A/B v3 supports exactly `pg_config` or
`native_toolchain`; the latter requires the dedicated `pgbench/source-patch`
spec and two explicit absolute trusted native bindirs.

## Descriptive offline imports

`benchmark import` works outside a scenario-pack workspace and never invokes a
load generator. It creates an immutable directory containing `result.json`, the
exact input bytes at `raw/source`, and, for the generic legacy
`hammerdb6`/`benchbase` adapters, the exact mapping at `raw/mapping.json`.
SHA-256 digests and byte sizes bind every retained input.
`benchmark import-verify` rejects extra/symlinked files, rechecks every digest,
strictly re-parses the source, and independently rebuilds the normalized
artifact. The directory is path-independent. `benchmark import-bundle` adds a
deterministic archive and closed inventory; `import-verify --bundle` requires
that inventory and repeats normalization after relocation.

```bash
pgworkbench benchmark import sysbench1 \
  --workload oltp_read_write/postgresql \
  sysbench.txt imported/sysbench

pgworkbench benchmark import hammerdb6 \
  --manifest hammerdb-mapping.json \
  hammerdb-job-report.json imported/hammerdb

pgworkbench benchmark import benchbase \
  --manifest benchbase-mapping.json \
  benchbase-result.json imported/benchbase

# Pinned upstream formats need no operator mapping or workload override.
pgworkbench benchmark import hammerdb6report \
  hdb_JOBID.json imported/hammerdb-v6
pgworkbench benchmark import benchbase33c0047 \
  tpcc.summary.json imported/benchbase-33c0047

pgworkbench benchmark import-verify imported/hammerdb/result.json
pgworkbench benchmark import-bundle \
  imported/hammerdb generated/hammerdb-import.tar.gz
pgworkbench benchmark import-verify --bundle \
  <extracted-root>/imports/<artifact-digest>
```

The sysbench parser accepts the official 1.0 console-summary shape and requires
version, complete General statistics, total events/time, and average/p95
latency. It uses reported transactions/s or events/s when present, otherwise
derives events/s from the two reported totals. Parser v1.1 also checks a
reported rate against total events and elapsed time with a bounded 0.1%
timer-boundary allowance plus printed-decimal rounding; a syntactically valid
but inconsistent rate fails. Unknown major/minor versions, duplicates,
truncation, localized labels, malformed numbers, and unsafe text fail closed.

Two pinned layouts are parsed without an operator mapping. Adapter
`benchbase33c0047` accepts the exact `ResultWriter` `*.summary.json` layout at
commit `33c00473807ebd49304d114a6d769d2d2b2bbb34`; it requires `POSTGRES`, final
state `DONE`, positive request/elapsed totals, chronological timestamps, and
recomputes throughput from requests and nanoseconds within a strict floating
point tolerance. Adapter `hammerdb6report` accepts the
`hammerdb-job-report-v1` layout at HammerDB v6.0 commit
`d33f879aec858063edd17aa2daa46db03abb2bae`; it requires the public/redacted,
unaudited/non-TPC disclaimer, PostgreSQL, TPROC-C or TPROC-H, a complete
workload-specific result/config shape, and positive reported metrics.

HammerDB does not report an exact workload start/end pair in this artifact.
TPROC-C timing is therefore labelled `declared-window` from
`duration_minutes`; TPROC-H timing is labelled
`reported-aggregate-query-time`. Neither pinned report supplies an exhaustive
error channel, so `errors.complete=false`: a normalized zero is not a claim of
zero upstream errors. The legacy `hammerdb6` and `benchbase` adapters remain
available for other structured layouts and require an explicit RFC 6901 JSON
Pointer mapping. All modes prove retained-byte integrity and deterministic
normalization only, not upstream execution, workload fairness, database
identity, or result validity.

All imported artifacts are permanently `descriptive-imported`, with
`decision_eligible=false` and `pgbench_series_eligible=false`. They live outside
`runs/benchmarks`, do not implement the pgbench series contract, and cannot
enter `benchmark compare`, history, or the AB/BA decision path. They make no TPC
compliance or cross-system comparison claim.

## Pinned native external-driver execution

`benchmark driver-run` executes the BenchBase, HammerDB, or sysbench record from
`compatibility/benchmark-drivers.json`. The command constructs the complete
argument vector itself and invokes the selected executable directly; there is
no shell string, arbitrary `--arg`, inherited environment, or overwrite path.
Execution contract v2 also requires an absolute non-symlink `--runtime-root`.
The caller supplies a regular executable, config, and adapter-specific workload
script/JAR or reviewed marker. Each adapter enforces the required relation to
that root: BenchBase's JAR is inside it while Java is external, HammerDB's
launcher is its only file, and sysbench's executable and Lua files are inside
it. Before execution the runner copies the selected driver closure below
`inputs/runtime/`, normalizes its
retained modes, records a sorted file inventory and tree digest, and verifies
the staged tree both before and after the process. A bounded process group and
timeout prevent a driver from leaving accepted background descendants.

```bash
pgworkbench benchmark driver-run \
  --acknowledge-external-disposable-target \
  --driver sysbench-postgresql-1.0.20 \
  --runtime-root /opt/pgworkbench/drivers/sysbench-1.0.20/runtime \
  --binary /opt/pgworkbench/drivers/sysbench-1.0.20/runtime/bin/sysbench \
  --config sysbench-run.json \
  --script /opt/pgworkbench/drivers/sysbench-1.0.20/runtime/share/sysbench/oltp_read_write.lua \
  --workload oltp_read_write/postgresql \
  --timeout 20m generated/sysbench-execution

pgworkbench benchmark driver-run \
  --acknowledge-external-disposable-target \
  --driver benchbase-postgresql-33c0047 \
  --runtime-root /opt/pgworkbench/drivers/benchbase-33c0047/benchbase-postgres \
  --binary /opt/pgworkbench/drivers/temurin-23.0.2+7-jre/bin/java \
  --config tpcc-postgres.xml \
  --script /opt/pgworkbench/drivers/benchbase-33c0047/benchbase-postgres/benchbase.jar \
  --workload tpcc \
  --timeout 2h generated/benchbase-execution

marker_file="$(mktemp)"
chmod 0600 "$marker_file"
printf '%s\n' 'pgworkbench.hammerdb-v6-execute-only-template/v1' > "$marker_file"
PGWORKBENCH_DRIVER_PASSWORD='use-a-secret-source' \
pgworkbench benchmark driver-run \
  --acknowledge-external-disposable-target \
  --driver hammerdb-postgresql-6.0 \
  --runtime-root /opt/pgworkbench/drivers/hammerdb-6.0/runtime \
  --binary /opt/pgworkbench/drivers/hammerdb-6.0/runtime/hammerdbcli \
  --config configs/benchmark-drivers/hammerdb-v6-tprocc-postgresql.json \
  --script "$marker_file" \
  --workload tprocc/postgresql \
  --timeout 20m generated/hammerdb-execution
rm -f "$marker_file"

pgworkbench benchmark driver-run-verify generated/sysbench-execution
```

Every external execution requires
`--acknowledge-external-disposable-target`. This is a recorded operator
assertion that the target is disposable and non-production; workbench does not
own, authenticate, inspect, or attest that claim. The target guard accepts no
remote override: the retained driver config must resolve syntactically to
exactly numeric loopback `127.0.0.1` or `::1`; hostnames are refused so a
resolver cannot redirect the run. The target database must not be `postgres`,
`template0`, or `template1` (case-insensitive). BenchBase must
contain exactly one unescaped `jdbc:postgresql://host[:port]/database` URL with
no userinfo, query, fragment, multiple host, or nested XML content. The
extracted host/port/database, fixed policy, acknowledgement, and false
ownership/identity-attestation flags are bound into `execution.json` and
independently re-derived by verification.

The sysbench config is a closed
`pgworkbench.sysbench-native-run-config/v1` JSON document. It fixes threads,
duration, report interval, rate, random seed, and PostgreSQL host/port/user/db.
If `PGWORKBENCH_DRIVER_PASSWORD` is set, the value is written only to an
ephemeral mode-`0600` pgpass file and never enters argv or retained evidence.
The BenchBase XML must contain no embedded secret-like value; use the exact
`{{PGWORKBENCH_DRIVER_PASSWORD}}` placeholder in a password element. The runner
materializes that substitution only below its ephemeral private work directory.
It always forces `--create=false --load=false --execute=true`, so schema/data
preparation remains an explicit operator step rather than an unrecorded phase.
The BenchBase runtime root must contain `benchbase.jar`, its complete transitive
manifest `Class-Path` closure, and `config/plugin.xml`. The selected JARs and
plugin file are staged with their relative paths and independently re-derived
by verification; unrelated files in the source distribution are outside the
claim. The Java executable itself is retained and byte-checked before and after
execution, but its surrounding JRE is not captured.

The HammerDB config is a closed
`pgworkbench.hammerdb-v6-native-run-config/v1` JSON document for exactly one of
`tprocc/postgresql` or `tproch/postgresql`. Its mode is fixed to
`execute-only-prepared-schema`; build, load, check, delete, caller Tcl,
transaction-counter agents, and metrics agents are not accepted. `--script`
must be a private ephemeral file containing only the exact marker
`pgworkbench.hammerdb-v6-execute-only-template/v1` plus a newline. The marker is
an adapter API token, not Tcl; it is intentionally not shipped as a standalone
config file. The hosted release helper creates it under disposable runner state.
The adapter generates the Tcl itself: PostgreSQL/workload settings, `loadscript`,
`vucreate`, `vurun`, exact parsing of `Benchmark Run jobid=<24 uppercase hex>`,
`vudestroy`, and `jobs $jobid save`.

HammerDB password bytes are passed only through the minimal
`PGWORKBENCH_DRIVER_PASSWORD` process environment and assigned directly to its
ephemeral in-memory config dictionary. They are never passed through `diset`
(which prints values), argv, retained config/template, or generated Tcl. The
run is refused if the bytes appear in stdout, stderr, or the saved public
report. Empty stderr, exactly one adapter job-id/report marker, exactly one
matching `hdb_<jobid>.json`, and an identical report `job.jobid` are required.
The strict `hammerdb6report` parser then independently requires the redacted
non-TPC v6.0 shape; `errors.complete` remains false.

The HammerDB runtime root must contain exactly one regular executable named
`hammerdbcli`; the adapter executes the staged retained copy from an isolated
working directory. The supported PostgreSQL CLI path uses the launcher's
embedded Tcl/Pgtcl payload, so the unrelated upstream GUI, Python, agent,
documentation, and source trees are not accepted into this root. This does not
attest the launcher's host dynamic libraries or establish how it was built.

For sysbench, `--binary` and `--script` must both be below the runtime root. The
adapter stages `bin/sysbench`, the workload-selected Lua file, and sibling
`oltp_common.lua`, then supplies a fixed `LUA_PATH` into the staged tree. The
binary therefore executes the retained Lua closure rather than silently
falling back to its compiled installation prefix. libpq, OpenSSL, libaio, libc,
and other host dependencies remain outside the closure.

The immutable output contains `execution.json`, a closed `inventory.json`, the
retained driver lock and inputs, the adapter-selected `inputs/runtime/` tree,
stdout/stderr, the exact selected upstream result, and `normalized-import/`.
`execution.json.inputs.driver_runtime` binds its strategy, root, entrypoint,
sorted path/digest/size/mode records, file count, total size, and canonical tree
digest. BenchBase requires exactly one
`*.summary.json`; sysbench requires empty stderr and normalizes its exact
stdout. HammerDB requires one job-id-bound saved report and no secondary error
channel. Verification rehashes the closed file set, independently reconstructs
the driver-specific runtime closure, re-parses the retained lock, reconstructs
the adapter-owned argv/Tcl from retained non-secret inputs, rechecks target
safety and exposed result/config fields, and repeats strict import verification
from the retained result bytes.

This envelope attests the retained adapter-selected runtime closure and binds
its observed bytes to a pinned registry record. It deliberately does **not**
prove that the supplied executable or JAR was built from that source commit, or
that the host interpreter and dynamic libraries match an upstream
distribution. Consequently `driver_runtime_closure_attested=true` coexists with
`source_to_binary_attested=false` and
`host_runtime_dependencies_attested=false`. HammerDB's public report does not
expose every execution input: TPROC-C
warehouses/virtual users/ramp-up/duration and TPROC-H scale/query-set count are
cross-checked, while TPROC-C total iterations and TPROC-H virtual users/degree
of parallelism remain input-to-generated-Tcl evidence, not result-proven facts.

The result remains
`descriptive-external-single-trial`: not a pgbench series, not eligible for
`benchmark compare` or AB/BA decisions, not TPC compliant, and not a
cross-system leaderboard row. Exact protected-runner acquisition, layout, and
database-preparation requirements are in
[external-driver-runner.md](external-driver-runner.md); that provisioning
document is not proof that the runner or a live release gate exists.

## Ordered benchmark campaigns

`benchmark campaign-run` snapshots an ordered list of benchmark protocols
before execution and runs each as a fresh independent series on one selected
runtime. It is the convenient way to capture a saturation curve or a bounded
workload pack without turning unlike protocols into one score:

```bash
pgworkbench benchmark campaign-run --runtime native \
  --campaign-id saturation-local --subject host-calibration \
  pgbench/saturation/c01 pgbench/saturation/c04 \
  pgbench/saturation/c16 pgbench/saturation/c64
pgworkbench benchmark campaign-verify saturation-local
pgworkbench benchmark campaign-bundle \
  saturation-local generated/saturation-local.tar.gz
```

The campaign retains failed, invalid, and unavailable rows and continues with
later independent rows. Verification reopens every available series and
re-derives the campaign. Its deterministic bundle includes the campaign, all
verified series, and every linked experiment run; `campaign-verify --bundle`
requires the relocated inventory. The artifact is always descriptive, has no
aggregate score or winner, and cannot enter the A/B decision path.

For repeated compatible observations of the same protocol,
`benchmark history-create` builds a chronological descriptive history with
normalized change from the previous and first observations. It verifies and
bundles the same transitive closure and similarly makes no causal verdict. A
history is bound to one canonical environment-population digest. It covers the
container image, native toolchain bytes, PostgreSQL server version and
configuration, measured endpoint, engine bytes, pack, and every other runtime
field; only the self digest and the run-specific location of an independently
verified native snapshot are normalized. Any substantive change starts a
different population and is rejected instead of being folded into one timeline.

## Assurance boundary

A benchmark artifact applies only to the source revisions, scenario, dataset,
PostgreSQL and driver versions, configuration, runtime, protocol, and trial
population it actually records. The current runtime fingerprint is deliberately
bounded: runtime kind, producer OS/architecture, driver and parser versions,
observed PostgreSQL server version, PostgreSQL configuration digest, engine
version/commit plus the retained `pgworkbench` executable digest, pack identity,
and qualification. Docker trials additionally record the local Compose driver
and target image IDs reported at execution time. It is not an exact description
or independent attestation of the host, image bytes, container layers,
firmware, storage,
thermal state, clock quality, interference, or client placement.

- `passed` means the series met its execution, parsing, integrity, minimum-valid,
  and variability gates. It is not a performance-win verdict.
- `benchmark run-verify` is the explicit series-integrity check; successful
  execution alone should not be reported as independent artifact verification.
- `unqualified-local-smoke` proves only that the bounded integration path ran
  and its resulting artifact can be checked.
- Native and Docker executions are distinct runtime populations. Results from
  them are not performance-equivalent and fail comparability checks.
- PgBouncer benchmark execution is Docker-only. Its endpoint contract fixes
  `postgres` as the pgbench driver service and `pgbouncer:5432` as the measured
  endpoint; direct Docker uses `127.0.0.1:5432` inside `postgres`. Native
  supports only the guarded owned direct endpoint and rejects the proxy target
  before creating a series directory.
- A Docker image ID segments the trial population by the local identity Compose
  reported. The artifact does not retain or independently rehash image config
  or layer bytes and does not attest a registry repository digest, image
  publisher, signature, build recipe, or supply-chain provenance; mutable tags
  are therefore recorded as reported local identity, not upgraded into
  authenticity evidence.
- The PgBouncer topology spec and normalized settings passed by the runner are
  protocol/execution-digest bound. Evidence retains and cross-checks the local
  driver and target image IDs reported in the transcript, but does not
  authenticate image bytes, registry origin or publisher, attest Docker
  networking, or prove absence of host noise.
- A comparison is rejected when required identity or protocol fields differ.
- A series is initialized under a sibling staging directory and published with
  one rename only after protocol, engine, native-toolchain, and pack inputs have
  been captured. A/B and campaign containers use the same initial-publication
  rule. Runtime failures after publication remain terminal evidence rather than
  being erased.
- A verified host-qualification JSON document proves only structural and
  recorded-content consistency. It does not prove host identity, exclusive or
  dedicated ownership, current state, or collection provenance.
- Production targets, capacity promises, SLAs, universal tuning advice, and
  conclusions about a different workload remain out of scope.

## Current pgbench evidence

Each trial invokes warm-up separately from the measured pgbench command. Only
the final measured summary and measured transaction logs enter the normalized
trial. The strict final-summary parser covers representative PostgreSQL 15-18
output and current PostgreSQL 19 development output. Unknown, localized,
contradictory, incomplete, or unsupported output makes the attempt invalid
rather than silently substituting zero.

Seed semantics are exact protocol identity. Without `BENCHMARK_RANDOM_SEED`,
the plan records `client-random-default` and pgbench chooses its streams. With
seed `N`, the plan records `phase-split-offset-v1`: measure uses `N`, warm-up
uses `N+1`, and `9223372036854775807` wraps the warm-up seed to `0`. Both
derived seeds are serialized and verified; warm-up and measurement therefore
never accidentally replay the same declared stream.

When transaction logging is enabled, all plain worker logs are retained and
combined into one `pgworkbench.pgbench-log-result/v1` record for the trial. It
records:

- logged, completed, failed, skipped, retried, and total-retry counts;
- latency in microseconds as count, minimum, mean, p50, p95, p99, and maximum;
- the same distribution for schedule lag when a rate-limited invocation
  declares that column;
- whether the evidence is sampled, without extrapolating sampled counts.

The parser rejects aggregated logs and `--failures-detailed` status layouts in
the current slice. Because an optional seventh numeric column is ambiguous,
schedule-lag and retry layouts are derived from the immutable plan rather than
guessed from the file. With a full sample, normalized transaction counts are
cross-checked exactly against the final summary. The transaction-log mean
latency is also cross-checked against pgbench's independently computed summary.
Without throttle, progress, or a latency limit, pgbench derives that summary
from its global client-time window while the log contains per-client transaction
intervals. The raw mean must not exceed the global mean, and their bounded
client-loop/start-stop gap may be at most two percent plus the printed 0.001 ms
rounding interval. Detailed summaries must match the log at printed precision.
Neither path rewrites either measurement. The driver-observed PostgreSQL server major must match the linked
experiment fingerprint. Nonzero failed, skipped, or retried transactions
currently fail the trial validity gate even though their counts remain
preserved.

The full artifact lifecycle, directory tree, and implemented benchmark JSON
contracts are documented in
[evidence-format.md](evidence-format.md).

## Trial and minimum-valid semantics

The smallest independent observation is one complete measured trial prepared
under the declared reset policy. Transactions and latency rows inside that trial
are correlated observations, not independent benchmark repetitions.

The producer schedules the declared number of trials sequentially. A failed
experiment attempt marks the series failed and stops further execution. A
protocol-invalid attempt is retained with its objective reasons and execution
continues. Invalid attempts are excluded from aggregate statistics; the series
may still pass when all of these conditions hold:

- the valid count reaches `BENCHMARK_MIN_VALID_TRIALS`;
- a measurement-class series has at least five valid trials;
- no execution attempt failed; and
- the valid-trial CV does not exceed `BENCHMARK_MAX_CV_PCT`.

When the minimum is reached with retained invalid attempts, the series records
that fact in `reasons`. If valid trials are below the minimum, the series is
`invalid`; if CV is above the declared maximum, it is `inconclusive`.

All valid trial values remain in the aggregate. Automatic outlier removal is
forbidden. Robust summaries or flags may aid diagnosis, but must not silently
change the sample used by a gate. This follows the distinction between robust
descriptive methods and deletion of inconvenient measurements; see the NIST
guidance on the [median absolute deviation](https://www.itl.nist.gov/div898/handbook/eda/section3/eda35h.htm).

## Calibration packs

`pgbench/smoke` is a two-second, one-trial contract smoke and emits
`unqualified-local-smoke`. The built-in `pgbench/read-only`,
`pgbench/read-write`, and `pgbench/custom-transfer` packs use ten planned
trials, eight required valid trials, a ten-second warm-up, and a 60-second
measurement. They are **calibration examples** for parsing, repetition,
variance, invalid-attempt retention, and portable artifacts. Their duration,
scale, client count, threshold, and reset policy are not qualified defaults for
a real performance claim.

For a real study, calibrate duration and load from the workload and host rather
than copying those values. PostgreSQL's pgbench documentation recommends runs
lasting at least several minutes for useful measurements and warns that the
default scale is often too small for modern hardware. That guidance does not by
itself qualify a workbench result.

## Built-in pgbench study packs

The repository also ships longer, still-unqualified study templates. They use a
30-second warm-up, a 180-second measurement window, ten planned trials, a scale
factor of 100, deterministic phase-split seeds, and sampled raw transaction
logs. Those values make the protocol concrete; they are not universal capacity
or SLO defaults.

| Pack | Implemented question and bounded gate |
| --- | --- |
| `pgbench/saturation/c01`, `c04`, `c16`, `c64` | Four fixed closed-loop read/write concurrency points. Each point yields its own verified series. Client count is protocol identity, so cross-point curves are descriptive and must not be presented as a causal A/B comparison. |
| `pgbench/rate-limited-slo` | Open-loop 1000 TPS probe with a 50 ms pgbench latency limit. A trial is invalid if failures, retries, or skipped work occur, or if more than the predeclared 1% of completed transactions exceeds the limit. Schedule-lag evidence is retained. The numeric target is a template input, not an SLA. |
| `pgbench/connection-churn` | Select-only work with one database connection per transaction. `--connect` is a typed, digest-bound protocol field; verification requires reconnect-specific average connection time and including-reconnection TPS evidence. Mean latency is primary because `pgbench.tps` retains its existing excluding-connection-setup meaning and is never aliased to reconnect TPS. |
| `pgbench/pgbouncer/direct-smoke`, `proxy-smoke` | One-trial contract smokes for the direct and pooler endpoint mappings. They exercise routing and evidence portability only; they are not performance evidence. |
| `pgbench/pgbouncer/direct-connection-churn`, `proxy-connection-churn` | Docker connection-churn measurements with identical pgbench load shape but distinct digest-bound targets. Initialization, controls, PostgreSQL metrics, and correctness checks remain direct to the owned backend; only warmup/measure pgbench traffic uses the selected endpoint. |
| `pgbench/wal-checkpoint-fsync-baseline`, `pgbench/wal-checkpoint-fsync` | The same TPC-B-like custom script, including an explicit checkpoint on approximately one transaction in 1000, under default versus deliberately checkpoint/fsync-heavy PostgreSQL config. Zero driver failures is the correctness oracle; WAL counters and snapshots are retained. The current collector does not claim a normalized checkpoint or fsync delta. |
| `pgbench/source-patch` | One identical pgbench/config protocol used for both arms of native-toolchain A/B. The subject is a bounded seven-executable byte set with matching observed versions for all seven tools, not a config or label. Adjacent installation/system dependencies, source commit, build provenance, and patch causality are not attested. |

The WAL/checkpoint script executes `CHECKPOINT` and therefore assumes the
privileged role owned by the disposable workbench runtime. It is deliberately
unsuitable for a production target. Run the saturation points explicitly, for
example:

```bash
for spec in c01 c04 c16 c64; do
  pgworkbench benchmark run --runtime native "pgbench/saturation/$spec"
done
```

Run the direct and PgBouncer rows as an ordered descriptive campaign:

```bash
pgworkbench benchmark campaign-run --runtime docker \
  --campaign-id pgbouncer-connection-paths --subject local-calibration \
  pgbench/pgbouncer/direct-connection-churn \
  pgbench/pgbouncer/proxy-connection-churn
```

The campaign has no aggregate winner. `BENCHMARK_TARGET` participates in both
identity digests, so ordinary comparison and the current explicit-subject
A/B contract cannot silently treat direct PostgreSQL and PgBouncer as one
population. A causal direct-versus-pooler conclusion would require a separate,
qualified, counterbalanced target-difference protocol. These packs do not
claim equivalence, improvement, or capacity.

Five non-TPS packs now use the separate descriptive series contract in
[operation-benchmarks.md](operation-benchmarks.md): both massive-DML bulk-load
index strategies and bracketed manual vacuum run on Docker/native, while
logical-marker convergence and multi-version dump/restore run on Docker. The
replication metric is a polling-inflated upper bound and the upgrade metric is
complete linked-run wall time; neither is pure apply latency, `pg_upgrade`,
recovery, RTO, or SLA evidence. Native PostgreSQL executable-byte-set subjects
use the strict A/B v3 path.

Operation trials run with `.env.example` in a clean runner-owned child
environment. Their independently recomputable static-input capsule excludes
private/runtime-output roots and is capped at 1,024 files/64 MiB. Each series
retains and boundary-revalidates the executing `pgworkbench` bytes; a native
series additionally retains and boundary-revalidates exactly seven PostgreSQL
executables. Relocated bundle verification is bound to the inventory's
canonical series path. These controls provide bounded internal consistency,
not a process sandbox, complete dependency/build provenance, host attestation,
or authentication of the unsigned self-signed bundle inventory.

The same series-local closure applies to every ordinary native pgbench series.
When such a series is produced inside native-toolchain A/B, the external
arm-level snapshot is first verified as the protocol binding and the selected
bytes are then copied into that series' canonical
`protocol/native-toolchain/` snapshot. Generic series verification requires
the local copy; A/B verification requires both levels and rejects drift at
either one. This duplicates bounded identity evidence for relocation and does
not extend it into source, build, library, or host provenance.

Broader bloat/autovacuum, temporary/parallel-query,
physical-replication, and deeper upgrade protocols remain evidence-driven
extensions rather than implied capabilities.

## Current comparison behavior

`benchmark compare` verifies both input series, requires distinct non-overlapping
measurement populations, enforces comparison/protocol identity, rejects native
versus Docker comparisons, and checks the allowed subject dimension. Invalid or
failed inputs cannot enter analysis, and an inconclusive input remains
inconclusive.

The analysis implementation compares medians and computes a deterministic
bootstrap confidence interval for normalized percentage change. That result is
descriptive regardless of any mutable qualification field: two independently
scheduled series can confound host drift with the subject change. Therefore a
pair of otherwise compatible artifacts returns `inconclusive` and cannot
conclude `improved`, `regressed`, `equivalent-within-threshold`, or
`no-regression`.

Bootstrap intervals are an analysis tool rather than a cure for poor protocol
design; see the NIST [bootstrap plot](https://www.itl.nist.gov/div898/handbook/eda/section3/bootplot.htm)
discussion. The only performance-decision path is the counterbalanced
AB/BA protocol in [benchmark-ab.md](benchmark-ab.md), with a complete fixed
qualification policy and before/after bookends.

## Decision protocol

This section describes the implemented evidence components and counterbalanced
producer. Component availability alone does not open the performance-decision
gate; the verifier independently requires every recorded gate.

### First-class phases

Each trial now records canonical RFC3339Nano start/end timestamps,
wall-derived `duration_ns`, and `passed|failed|skipped` status for these ordered
phases:

1. **Preflight**: isolation, required tools, clocks, resource headroom, identity,
   and comparability fields.
2. **Prepare**: declared dataset creation/restore, exact configuration, driver
   input staging, and creation of driver-owned temporary log locations.
3. **Stabilize**: explicit recovery, checkpoint, replica, background, and
   resource conditions rather than an unexplained sleep.
4. **Pre-warmup control**: an exact before-warmup statistics reset when the v2
   protocol declares it; otherwise an explicit skipped event.
5. **Warm up**: the declared cache/workload regime; samples excluded from the
   measured result.
6. **Pre-measure control**: the exact before-measure statistics reset, when
   declared, followed by the protocol-bound cache observation. A v2 run cannot
   enter measurement if this boundary fails.
7. **Measure**: only the immutable `pgbench` invocation for the declared time
   or work budget; reset and cache-control work is excluded by construction.
8. **Cool down**: stopped clients and any required asynchronous-work boundary.
9. **Validate**: correctness, error-budget, saturation, and protocol gates.
10. **Collect**: sealed driver, PostgreSQL, host, and normalized evidence. The
   Docker `pgbench` adapter copies raw transaction logs here only after the
   preceding lifecycle has succeeded.
11. **Clean**: only resources owned by the disposable run, including staged
   `pgbench` scripts and container raw-log directories on every terminal path.

The normalized timeline is mandatory, has a deterministic digest, and is
checked against the raw phase journal. Each v3 row binds the exact linked
`run_id` and trial number. A passed trial carries a digest-and-size
`phase_journal` reference to `artifacts/benchmark/phases.tsv` in the linked run;
the series copy is required to be byte-identical. Cleanup cannot be skipped; failed and
skipped events require reasons, and events after a failure follow the closed
skip transition. A failed timeline cannot support a passed trial. Runner-owned
deadlines and bounded process-group termination close or recover the terminal
failed verdict when an owned manifest exists. On the passing benchmark path,
collect and cleanup are sealed before the terminal passed verdict is written.
Protocol-v2 typed control artifacts are materialized from their raw sources
after the final cleanup event and before that verdict; later series
normalization is read-only.

The Go benchmark runner creates the empty series mirror. Once the experiment
shell reserves the linked run, it moves ownership to the primary linked-run
journal and appends every row to both files;
the experiment shell owns the preflight decision. It installs terminal cleanup
before loading repository or experiment input and, after reserving the trial's
canonical run directory, publishes a conservative manifest. A repository-env,
spec-load, state-writer, target, or hook-trust rejection therefore closes all
eleven phases and publishes a verifiable failed linked run.

One fail-closed exception is unavoidable: if `runs/<trial-id>` already exists,
the runner never overwrites or augments that immutable evidence. The rejected
attempt still closes its deterministic series-owned phase journal and trial
record, but it cannot truthfully publish a second linked artifact at the same
canonical path. That conflict keeps the series non-passing and requires a new
unique series/run id for a retry.

### Counterbalanced A/B design

The A/B runner predeclares an even alternating sequence of `AB` and `BA`
blocks, a minimum complete-unit count, analysis seed, confidence level,
bootstrap count, and practical threshold. One inference unit contains one AB
and one BA block; the cluster bootstrap resamples whole units. Running all
baseline trials and then all candidate trials, as two independent current
series, is not counterbalanced evidence and can confound host drift with the
subject change. The closed v3 design and schemas are in
[benchmark-ab.md](benchmark-ab.md).

A/B protocol v3 derives the exact sorted union of settings assigned by the two
snapshotted PostgreSQL config profiles. Every trial records only those
effective `pg_settings` rows during prepare. Both subject modes require stable
rows within each arm, one observed server version across arms, and no pending
restart. `pg_config` additionally requires at least one cross-arm
value-and-unit difference. `native_toolchain` instead requires byte-distinct
seven-file snapshots and matching observed versions for all seven tools,
without inventing an unrelated GUC difference. Raw evidence is
fixed-path, bounded, run/protocol/trial bound, and independently re-parsed.

### Reset, cache, and collectors

Benchmark plans now require cache regime, cumulative-statistics reset policy
and boundary, collector set and interval, collector-overhead treatment, client
placement, and resource-budget mode. These values are normalized into both the
protocol digest and comparison-key digest, so differing declarations cannot be
compared as the same protocol. The v1 collector set is exactly
`pgbench-driver` plus `postgresql-sampler-v1`; its interval is passed to the
sampler, verified coverage must span the measurement window, and consecutive
selected samples may be separated by at most two declared intervals. This
explicit allowance covers bounded query/scheduler and terminal-boundary jitter;
it is part of the normalized evidence rather than an inferred tolerance.

This identity is deliberately not blanket enforcement. Cache regime is
recorded but the runner does not flush or inspect PostgreSQL/OS caches;
restarting a container does not establish `cold`. Statistics reset is either
`none` or explicitly `operator-managed`, and the latter is not executed or
verified. Collector overhead is declared as included/unquantified or
operator-calibrated, without treating the latter as a bundled calibration
proof. Resource limits are either explicitly unbounded or carry
operator-declared CPU-core and memory-MiB values; v1 does not apply cgroups or
OS limits. Client placement is a declaration in ordinary runs and is checked
against the strict qualification gate only in counterbalanced A/B runs.

#### Explicit protocol v2 controls

`BENCHMARK_CONTRACT_VERSION=2` is an explicit source-spec opt-in. Omitting it
continues to select v1; no legacy `cold`, `warm`, `steady`, operator-managed
reset/calibration, or operator-declared resource value is reinterpreted as a
v2 control. A v2 plan has the exact collector set `pgbench-driver` plus
`postgresql-sampler-v2` and supports only these bounded modes:

The two short opt-in contracts exercise the complete path. The first is valid
on native PostgreSQL or Docker with explicitly unbounded resources; the second
also gates the exact Docker single-container cgroup-v2 provider:

```bash
pgworkbench benchmark run --runtime native pgbench/control-v2-smoke
pgworkbench benchmark run --runtime docker pgbench/control-v2-smoke
pgworkbench benchmark run --runtime docker pgbench/control-v2-docker-enforced-smoke
```

These are contract smoke tests, not performance evidence. The native adapter
rejects the Docker-enforced variant before reserving a series directory.

| Control | Supported v2 modes | What a satisfied artifact establishes |
| --- | --- | --- |
| Cache | `uncontrolled`; `postgres-shared-buffer-warm` with sorted target relations and a minimum resident percentage | For the warm mode, `pg_buffercache` main-fork resident-block counts for the exact database/relation OIDs met the threshold at the before-measure boundary. It says nothing about the OS page cache and does not claim a cold cache. |
| Cumulative statistics | `none`; `runner-managed` at `before-trial`, `before-warmup`, or `before-measure` | The runner completed the exact current-database and cluster-WAL reset calls and observed their PostgreSQL reset timestamps advance inside the recorded boundary. Metrics normalization permits one counter segment boundary only for the matching scope and timestamp; it sums observed pre-reset increments plus the current post-reset value. |
| Collector overhead | `included-unquantified`; `runner-calibrated-duty-cycle` with required sample count and maximum duty cycle | Calibrated rows are all regular samples on the exact declared `interval_ns` grid and correlate one-for-one with regular `metrics.csv` rows. Every actual row, including failed rows, is retained; at most one separately identified untimed terminal boundary row is allowed. A short or empty calibration remains verifiable evidence with `invalid-samples`, never a passing calibration. |
| Resources | `unbounded`; `runner-enforced` with positive integer CPU millicores and memory MiB | Only the exact `docker-single-container-linux-cgroup-v2` provider with `same-host` client placement is supported: PostgreSQL and pgbench share one container, Docker reports the requested limits, and the container reports cgroup v2. Native execution supports `unbounded` only and fails closed for runner enforcement. |

Runner-enforced resources are also rejected for the PgBouncer target: that
scope covers PostgreSQL plus the in-container driver, but not the separate
pooler container. PgBouncer packs therefore declare `unbounded` resources
until a provider can verify the complete multi-container scope.

Each linked trial stores four normalized JSON artifacts and their fixed raw
sources below `artifacts/benchmark/controls/`: `cache-state`,
`statistics-reset`, `collector-overhead`, and `resource-budget`. The JSON binds
the run, protocol digest, trial, control window, raw path/digest/size, derived
status/reasons, and a canonical self-digest. Verification rereads the bounded
raw source and independently re-derives the normalized observation and status;
a structurally valid but unsatisfied requested control cannot support a valid
trial.

The metrics summary itself binds the collector interval, observed/allowed
maximum gap, timing-control digest/raw source, reset policy/boundary/status,
per-scope reset timestamps, and whether each cumulative counter used one or two
segments. A reset-spanning delta is necessarily an observed lower bound because
activity between the last pre-reset sample and the reset timestamp is not
recoverable. Any second, wrong-scope, or unproven decrease remains invalid.

These are unsigned runner-recorded observations. Their digests establish
content integrity, not machine identity, privileged-host honesty, exclusive
resource ownership, absence of interference, or enforcement outside the
recorded PostgreSQL/Docker scope.

Driver measurements remain primary for throughput, latency, and errors.
Version-aware PostgreSQL snapshots may cover cumulative database, WAL, I/O,
checkpointer, replication, and optionally `pg_stat_statements` views. Host
collectors may cover CPU, memory pressure, storage latency/throughput, network
and client saturation, clocks, thermal state, and interference. Additional
collectors still need versioned implementations, phase windows, and separately
measured overhead. Expensive planning tracking, verbose logging, PoWA, and
pgBadger ingestion remain opt-in because they can alter the workload or depend
on cumulative state.

### Host qualification records

`benchmark host-inspect` and `host-verify` implement a versioned snapshot and
strict policy evaluator for CPU/governor, memory, storage/filesystem, kernel,
client placement, clocksource, thermal observations where available, resource
headroom, and interference. Missing required observations fail closed. Storage
paths, usernames, and host identifiers are not recorded.

The exact standalone artifact and CLI boundary is documented in
[benchmark-host-qualification.md](benchmark-host-qualification.md).

The artifact is unsigned and operator-recorded. Its digest protects content
integrity only; it does not establish host identity, dedicated ownership,
current state, or remote attestation. The A/B runner records and binds
matching before/after bookends using a complete fixed policy before paired
analysis may enter the decision gate. A standalone `qualified` verdict cannot
elevate ordinary `benchmark compare`.

An unavailable required observation on macOS or Linux fails its strict gate and
leaves the A/B result `inconclusive`; the runner never invents a platform value
or relaxes the predeclared policy.

## Adapter milestones

| Milestone | Deliverable |
| --- | --- |
| `pgbench` gold adapter | Complete phase/command/seed identity, counterbalanced execution, supported log modes, and bounded latency/error/retry/schedule-lag gates |
| Descriptive import contract | Implemented strict pinned parsers for BenchBase `33c0047` summary JSON and HammerDB v6.0 `hammerdb-job-report-v1`, plus sysbench 1.0 and generic mapped JSON; raw provenance and independent re-derivation, never series/decision evidence |
| BenchBase runner | Implemented bounded native single-trial envelope with fixed execute-only argv, staged manifest/JAR/plugin closure, and strict pinned summary normalization; broader phase-aware series integration remains out of scope |
| HammerDB runner | Implemented bounded v6.0 PostgreSQL TPROC-C/TPROC-H execute-only envelope with an exact staged launcher root, adapter-generated Tcl, and job-id-bound strict report normalization |
| sysbench execution adapter | Implemented bounded native single-trial envelope with staged executable/workload/common-Lua closure, fixed argv/Lua path, and strict console normalization; broader phase-aware series integration remains out of scope |

Useful PostgreSQL packs can extend the implemented saturation, open-loop SLO,
WAL/checkpoint/fsync, PgBouncer, descriptive bulk-load/manual-vacuum,
logical-marker-convergence, and multi-version dump/restore paths with broader
bloat/autovacuum coverage, temporary and parallel query work, physical
replication, and deeper upgrade protocols. Native executable-byte-set A/B is
implemented with a bounded seven-file identity. It does not bind adjacent
installation/system dependencies or prove source-patch causality. Each pack
must state its own correctness oracle, measurement basis, and practical
threshold.

## Primary references

- PostgreSQL [`pgbench`](https://www.postgresql.org/docs/current/pgbench.html),
  [cumulative statistics](https://www.postgresql.org/docs/current/monitoring-stats.html),
  and [`pg_stat_statements`](https://www.postgresql.org/docs/current/pgstatstatements.html)
- NIST/SEMATECH guidance on the
  [bootstrap plot](https://www.itl.nist.gov/div898/handbook/eda/section3/bootplot.htm)
  and [median absolute deviation](https://www.itl.nist.gov/div898/handbook/eda/section3/eda35h.htm)
- [BenchBase](https://github.com/cmu-db/benchbase) and the
  [OLTP-Bench paper](https://www.vldb.org/pvldb/vol7/p277-difallah.pdf)
- [HammerDB documentation](https://www.hammerdb.com/docs/), its
  [TPC comparison caveat](https://www.hammerdb.com/docs/ch03s04.html), and
  [HammerDB 6 result artifacts](https://www.hammerdb.com/blog/uncategorized/hammerdb-v6-0-tpc-oss-result-artifacts/)
- [sysbench](https://github.com/akopytov/sysbench)
- TPC [current specifications](https://www.tpc.org/tpc_documents_current_versions/current_specifications5.asp),
  [result submission rules](https://www.tpc.org/information/other/submit_results5.asp),
  and [Fair Use policy](https://www.tpc.org/tpc_documents_current_versions/pdf/tpc_fair_use_quick_reference_v1.0.0.pdf)
- [PostgresBench](https://github.com/ClickHouse/PostgresBench) and the PostgreSQL
  [Performance Farm](https://www.postgresql.org/developer/related-projects/)
- [PoWA](https://powa.readthedocs.io/en/latest/) and
  [pgBadger](https://github.com/darold/pgbadger)
