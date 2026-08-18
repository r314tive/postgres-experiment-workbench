# Counterbalanced A/B benchmark protocol

The current candidate implements the `pgworkbench.benchmark-ab-protocol/v3`
and `pgworkbench.benchmark-ab-run/v3` contracts. The purpose is narrow: decide
whether a predeclared PostgreSQL subject change has a practically important
effect under one recorded protocol. It is not a general benchmark leaderboard,
capacity promise, TPC result, host-ownership proof, or remote attestation.

Ordinary `benchmark compare` remains permanently descriptive. It analyzes two
independently scheduled series and therefore cannot separate subject effect
from time-ordered host drift. No qualification label, command-line switch, or
post-hoc metadata can elevate that path into a performance verdict. Only the
counterbalanced runner described here may enter the decision gate.

## Design in one picture

One inference unit contains both orders:

```text
before qualification bookend

unit 1:  block AB: baseline -> candidate
         block BA: candidate -> baseline
unit 2:  block AB: baseline -> candidate
         block BA: candidate -> baseline
...
unit N:  block AB: baseline -> candidate
         block BA: candidate -> baseline

after qualification bookend
```

The protocol serializes the complete order before execution. `AB` and `BA` are
not labels applied after results are known. Every block contains one fresh
baseline execution and one fresh candidate execution produced under the same
comparison key, runtime, primary metric, direction, and fixed analysis policy.
Those executions become distinct trials in the two role-specific series. A
complete unit is the smallest independent observation used by the paired
bootstrap; transactions, trials inside a series, and the two blocks inside one
unit are not additional independent units.

## Immutable protocol

[`benchmark-ab-protocol.schema.json`](../schemas/benchmark-ab-protocol.schema.json)
defines the closed protocol document. It binds:

- scheduler and run identity;
- one runtime and two explicit subject descriptors;
- the shared benchmark comparison-key digest;
- planned block count, minimum complete valid units, and the exact `AB`/`BA`
  order of every block;
- primary-metric direction and practical regression threshold;
- paired-analysis method, confidence level, bootstrap resample count, and seed;
- the complete host-qualification policy, a non-sensitive storage label,
  declared client placement, and maximum before/after bookend gap;
- the sorted union of PostgreSQL settings actually assigned by the two
  snapshotted configuration files, the fixed raw-evidence path, and the exact
  effective-settings parser version; and
- a deterministic content digest.

The storage path supplied to host inspection is an execution-time input. It is
never serialized into the protocol or run artifact. The storage label identifies
the recorded measurement class without leaking a username, mount path, or
temporary directory. Likewise, variant descriptors contain portable references
and digests, not credentials or arbitrary shell parameters.

Protocol fields are fixed before the first qualification bookend. Changing an
order, threshold, minimum population, seed, qualification gate, subject input,
or comparison key creates a different protocol digest and requires a new run.
There is no optional stopping rule in v3.

The benchmark comparison identity also fixes cache/reset declarations, the v1
collector set and interval, collector-overhead mode, client placement, and
resource-budget declarations. Both `ab-run` and the independent artifact
verifier require the declared client placement to equal the strict
before/after qualification gate. The other declarations remain
operator-recorded conditions: an A/B artifact does not prove a cold cache,
execute an operator-managed statistics reset, attest a calibration, or enforce
CPU/memory limits.

## Execution and retained evidence

The counterbalanced scheduler executes one block at a time and never reuses an
existing series as if it had been scheduled by this protocol. The baseline and candidate
observations accumulate in two ordinary immutable benchmark-series artifacts.
Each block records its declared order and points both executions to the exact
trial and linked experiment run in the corresponding series. The A/B result
binds both series references, result digests, and run IDs once at top level. A
block is valid only when both series verify, their trial populations are
distinct, the roles match the predeclared order, and every comparison-identity
field matches the protocol except the explicitly allowed subject dimension.

An invalid block stays in the artifact; it is not silently rerun, removed, or
reclassified as an outlier. If orchestration or persistence stops mid-block,
its incomplete zero-, one-, or two-entry execution record is retained, with
each surviving entry keeping its declared schedule position. Only a block with
exactly two valid executions is complete. A counterbalance unit is eligible
only when both its `AB` and `BA` blocks are complete and valid. Incomplete units
remain visible and are excluded as whole clusters. The decision gate requires
at least the predeclared number of complete units and never accepts fewer than
five.

The authoritative run document is defined by
[`benchmark-ab-run.schema.json`](../schemas/benchmark-ab-run.schema.json). It
binds the protocol, all block/series references, both qualification artifacts,
their bounded bookend assessment, the paired analysis, terminal status,
decision, reasons, and its own deterministic digest. Portable references must
remain below the inferred repository or extracted-bundle artifact root;
verification rejects absolute paths,
traversal, symlinks, digest mismatches, duplicate populations, and unlisted or
overlapping series.

## Explicit subjects and effective PostgreSQL settings

A/B v3 accepts exactly one subject dimension. `pg_config` retains the existing
strict path: different configuration names, bytes, and protocol digests are
necessary but not sufficient. `native_toolchain` instead requires the native
runtime, identical benchmark/config protocols, two byte-distinct absolute
bindirs, and identical observed version strings for all seven selected
PostgreSQL tools. The built-in path named `pgbench/source-patch` opts into that bounded
byte-set dimension; its name is not source or patch attestation.

Before artifact reservation, each bindir must contain executable, regular,
non-symlink `postgres`, `initdb`, `pg_ctl`, `pg_isready`, `createdb`, `psql`,
and `pgbench` files. V3 records every byte digest and bounded `--version`
observation, then copies those seven files as non-executable identity snapshots
under `toolchains/<role>/`. These snapshots close verification only over those
seven files; they are not a relocatable or complete PostgreSQL installation.
Adjacent `share`/`lib` content, dynamically loaded or system libraries, build
flags, and other runtime dependencies are not captured or attested. Execution
uses the original installation so its adjacent layout remains available. The
runner revalidates the original seven bytes before and after every arm, removes
duplicate ambient bindir entries, and stops the owned runtime using that arm's
`pg_ctl` before switching. Version output never becomes provenance: both
`source_commit` and `build_provenance` remain `unattested`.

Selecting a bindir authorizes the runner to execute all seven selected local
files with `--version` before artifact reservation. Each direct command has a
30-second context deadline, but this inspection is not a sandbox, signature,
build attestation, or complete descendant-process containment mechanism. Use
only trusted local installations. A resulting decision is bounded to the two
selected installations and recorded environment; it cannot attribute an
effect to a particular source patch.

For both dimensions, protocol v3 derives the sorted union of GUC names assigned
by the exact `postgresql.conf` snapshots. Each linked trial queries only those
rows from `pg_catalog.pg_settings` after PostgreSQL has restarted and retains
the bounded raw TSV at
`artifacts/benchmark/effective-pg-settings.tsv` and embeds a normalized,
digested record in the trial.

Every raw row binds the linked run ID, A/B protocol digest, trial number,
capture timestamp, and `server_version_num`, plus `name`, `setting`, `unit`,
`source`, `pending_restart`, and `context`. The capture must fall inside the
linked prepare phase and binds that phase journal's digest. Verification opens
the regular non-symlink raw file again, checks its digest and exact row set, and
independently re-parses it; stored normalized JSON is never authority by
itself.

The decision gate requires identical effective rows within each arm, one
observed `server_version_num` across both arms, and every `pending_restart`
value to be false. `pg_config` also requires at least one differing
`setting`+`unit` pair, so a comment-only or PostgreSQL-normalized equivalent
config stays inconclusive. `native_toolchain` does not require an unrelated
GUC difference. Missing, transplanted, extra, or tampered rows are invalid;
within-arm drift, a cross-arm server-version mismatch, or pending restart is
inconclusive.

The collector never dumps all settings. It records only protocol-named rows,
and refuses names likely to contain credentials, connection strings, secrets,
tokens, passphrases, or executable command text. This reduces disclosure risk;
it is not a general secret scanner or proof that arbitrary extension GUC values
are harmless.

## Qualification bookends

The runner records one host-qualification artifact immediately before the
scheduled blocks and another immediately after them. Both must independently
verify, have `verdict=qualified`, use the exact complete protocol policy, name
the declared storage label and client placement, and project to the same stable
recorded host class. The elapsed bookend interval must not exceed
`max_bookend_gap_seconds`.

These artifacts are deliberately unsigned and operator-recorded. Their hashes
protect recorded-content integrity; they do not prove physical host identity,
exclusive ownership, current state, collection provenance, or absence of a
noisy neighbour between samples. A successful bookend assessment is therefore
reported as `recorded-policy-passed`, not “attested”, “dedicated”, or “trusted”.
The A/B verdict is bounded to those recorded observations and the enclosed run.

A policy is complete for the v3 decision gate only when strict mode and all of
the following gates are fixed: minimum memory headroom, minimum storage
headroom, maximum load per CPU, required clocksource, required CPU governor,
and required client placement. Additional recorded gates may make the policy
stricter. Missing, unavailable, changed, or failed required observations close
the performance-decision gate.

Unavailable platform observations fail the corresponding strict gate rather
than being guessed. In particular, a macOS or Linux host that cannot report the
required clocksource, governor, placement, or headroom remains `inconclusive`;
platform name alone never waives the policy.

## Paired analysis and decisions

For each valid block, the normalized candidate effect is:

```text
higher-is-better: 100 * (candidate / baseline - 1)
lower-is-better:  100 * (1 - candidate / baseline)
```

Positive values mean improvement and negative values mean regression. V2 takes
the median of all valid block effects. Its percentile cluster bootstrap samples
complete AB/BA units with replacement, preserving the two order effects inside
each sampled unit. The seed, resample count, confidence level, bootstrap method,
and practical regression threshold are explicit immutable protocol fields; the
median estimator is fixed by the v3 schema and scheduler version.

Let `[low, high]` be the confidence interval and `T` the non-negative declared
regression threshold. After every execution, identity, population,
qualification, and analysis gate passes, the closed decisions are:

| Condition | Status | Decision |
| --- | --- | --- |
| `high < -T` | `failed` | `regressed` |
| `low > 0` | `passed` | `improved` |
| `low >= -T` and `high <= T` | `passed` | `equivalent-within-threshold` |
| `low >= -T` otherwise | `passed` | `no-regression` |
| interval crosses `-T` | `inconclusive` | `inconclusive` |

`invalid` means the artifact, protocol, execution population, or analysis input
violated a closed contract. `inconclusive` means valid bounded evidence was
insufficient to choose a performance decision. `failed/regressed` describes
the recorded metric and threshold only; it is not proof of a universal
PostgreSQL regression.

## Fail-closed decision gate

A performance verdict is possible only when all of these conditions hold:

1. Protocol and run artifacts independently verify and their digests bind.
2. The exact predeclared block order was executed without substituted or
   overlapping series populations.
3. At least `min_valid_units` complete AB/BA units are eligible.
4. Every series is measurement-class, passed, mutually comparable, and uses the
   declared runtime, comparison key, metric direction, and allowed subject
   difference.
5. Before and after qualification bookends independently verify, pass the same
   complete policy, match the recorded stable host class, bracket the run, and
   stay within the declared maximum gap.
6. The paired analysis is recomputed from referenced series rather than trusted
   from stored summary fields.
7. Every trial's narrow effective `pg_settings` evidence independently
   re-parses, remains stable within its arm, and has no pending restart. The
   both subjects prove one cross-arm server version; `pg_config` also proves a
   value-and-unit difference, while `native_toolchain` instead proves
   role-bound seven-file executable bytes with matching observed versions for
   all seven tools.

Any missing gate yields `invalid` or `inconclusive`; it never falls back to the
ordinary independent comparison. Smoke runs, Docker/native cross-comparisons,
operator-edited labels, one-sided `A...A then B...B` schedules, post-hoc
thresholds, and unsigned qualification by themselves cannot issue a verdict.

## CLI and artifact layout

The candidate implements the producer, reader, and independent verifier. The
runner accepts either two `pg_config`-differing specs or one identical
`native_toolchain`-enabled spec twice. A complete fixed policy must be supplied
before execution; representative invocation:

```bash
pgworkbench benchmark ab-run --runtime native --run-id config-ab \
  --strict \
  --storage-path /path/to/postgres-data --storage-label postgres-data \
  --client-placement same-host \
  --min-memory-available-pct 25 --min-storage-available-pct 30 \
  --max-load-1m-per-cpu 0.5 \
  --required-clocksource tsc --required-governor performance \
  --required-client-placement same-host \
  pgbench/baseline pgbench/candidate

pgworkbench benchmark ab-show --json <ab-run>
pgworkbench benchmark ab-verify --json <ab-run>
pgworkbench benchmark ab-bundle <ab-run> generated/config-ab.tar.gz

# Bounded same-version native executable-byte-set subject. This does not prove
# source/build identity, dependency identity, or source-patch causality.
pgworkbench benchmark ab-run --runtime native \
  --subject-dimension native_toolchain \
  --baseline-native-bindir /absolute/postgres-a/bin \
  --candidate-native-bindir /absolute/postgres-b/bin \
  [complete qualification options] \
  pgbench/source-patch pgbench/source-patch

# After extraction, require and verify the complete bundle inventory.
pgworkbench benchmark ab-verify --json --bundle \
  <extracted-root>/runs/benchmark-ab/<run-id>
```

The actual clocksource, governor, placement, headroom thresholds, storage path,
and storage label are operator inputs; the values above are illustrative, not
portable defaults. A terminal decision other than `passed` makes `ab-run`
return nonzero after preserving a completed artifact when finalization was
possible. `ab-show` reads the assertion; `ab-verify` independently reopens the
series and bookends, reconstructs blocks, reruns analysis, and derives the
terminal result.

The target live layout keeps the A/B orchestration artifact separate from the
ordinary immutable series that it references:

```text
runs/benchmark-ab/<run-id>/
├── protocol.json
├── result.json                       (authoritative blocks embedded)
├── blocks.tsv
├── summary.md
├── qualification/
│   ├── before.json
│   └── after.json
├── toolchains/                       (native_toolchain only)
│   ├── baseline/{manifest.json,bin/...}
│   └── candidate/{manifest.json,bin/...}
└── blocks/
    ├── 001-ab.json
    ├── 002-ba.json
    └── ...

runs/benchmarks/<run-id>-a/
└── <ordinary baseline benchmark-series tree>

runs/benchmarks/<run-id>-b/
└── <ordinary candidate benchmark-series tree>
```

All references in `result.json` are portable. Protocol and qualification file
references are relative to the A/B run directory; the two series references are
relative to the inferred repository or extracted-bundle artifact root. The A/B
bundle captures the full transitive closure: the A/B directory, both complete
series, and every linked experiment run. Relocated verification checks
containment and never depends on the producer's old absolute path.

The deterministic archive extracts as:

```text
pgworkbench-benchmark-ab-<run-id>/
├── benchmark-ab-bundle.json
└── runs/
    ├── benchmark-ab/<run-id>/...
    ├── benchmarks/<run-id>-a/...
    ├── benchmarks/<run-id>-b/...
    ├── <baseline-linked-trial-run>/...
    ├── <candidate-linked-trial-run>/...
    └── ...
```

Bundle creation first verifies the live A/B artifact and both series, rejects
symlinks/non-regular sources, copies the complete linked-run closure, sorts the
inventory by portable path, and records every file's size and SHA-256 digest.
The inventory follows
[`benchmark-ab-bundle-inventory.schema.json`](../schemas/benchmark-ab-bundle-inventory.schema.json)
and does not list itself. `ab-verify --bundle` requires it and fails on an
unsafe, missing, changed, duplicate, or extra regular file.

The producer, reader, live/bundle verifier, deterministic complete-closure
bundle, all three A/B JSON contracts, complete tamper matrix, and clean
relocated-bundle release gate are implemented in the candidate. The release
gate is exercised by `internal/benchmarkab/release_gate_test.go` and enters
`make release-check` through the Go test suite. Code in a working tree or one
locally verified run is still not published release or independent
reproduction evidence.
