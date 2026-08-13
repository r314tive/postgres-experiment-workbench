# Descriptive Operation Benchmarks

Not every useful PostgreSQL measurement is a transaction-rate workload. Vacuum,
bulk load, index build, replication convergence, and upgrade exercises need a
contract that preserves their real unit and scope instead of calling every
number TPS. `benchmark operation` is that separate contract.

Operation specs are strict JSON under `benchmarks/operations/**/*.json`. They
pin an existing experiment, exact trial count, runtime allow-list, primary
metric, unit, direction, measurement basis, variability ceiling, and assurance
boundary. Every spec and resulting series is permanently
`classification=descriptive-engineering` and `decision_eligible=false`.

```bash
pgworkbench benchmark operation list
pgworkbench benchmark operation show \
  massive-dml/offline-bulk-load-indexed

# Docker is the default and needs no host PostgreSQL installation.
pgworkbench benchmark operation run --runtime docker \
  massive-dml/offline-bulk-load-indexed

# Native execution requires one explicit, trusted PostgreSQL bin directory.
PGWORKBENCH_NATIVE_BINDIR=/absolute/postgresql/bin \
  pgworkbench benchmark operation run --runtime native \
  massive-dml/offline-bulk-load-indexed
pgworkbench benchmark operation run-show <series-id>
pgworkbench benchmark operation verify <series-id>
pgworkbench benchmark operation bundle \
  <series-id> generated/bulk-load-operation.tar.gz

# After extraction, require the closed inventory.
pgworkbench benchmark operation verify --bundle \
  <extracted-root>/runs/operation-benchmarks/<series-id>
```

The executable catalog currently covers:

- `massive-dml/offline-bulk-load-indexed` and
  `massive-dml/offline-bulk-load-index-after` on Docker or native;
- `maintenance/vacuum-bloat-manual` on Docker or native;
- `replication/logical-marker-convergence` on the owned Docker
  publisher/subscriber topology; and
- `upgrade/multi-version-dump-restore` on the owned Docker old/new-major
  topology.

The single-node operation profiles write exact result JSON to
`artifacts/operation-result.json`; the runner does not scrape stdout. `total_ms`
uses the PostgreSQL server clock around table creation, insertion, and required
index creation. `ANALYZE` is outside that interval and is named as excluded in
both spec and result. These are disposable engineering scenarios, not COPY
claims, TPC results, production capacity estimates, or universal advice.

The vacuum pack rebuilds a medium profile, creates committed churn, then uses
one psql session to record server time immediately before and after `VACUUM
(ANALYZE)`. Its scope explicitly includes client/server protocol gaps between
those bracketing commands and excludes churn. It is not a pure executor timer,
an autovacuum model, or a bloat-reclamation measurement.

The logical-replication pack inserts an ordered marker after a fixed mutation
set, waits for that exact marker on one subscriber, and then requires complete
publisher/subscriber table-signature equality. Its PostgreSQL-clock interval
starts before marker commit and ends after polling detects the row, so the
reported value is a polling-inflated convergence upper bound that includes
commit, apply, client round trips, and the fixed 100 ms polling cadence. It is
not pure WAL transport/apply latency, durability, or failover evidence.

The multi-version pack uses the linked-run wall clock because the intended unit
is the whole disposable rehearsal: topology reset/readiness, source setup,
logical dump, restore, assertions, evidence collection, and cleanup. It is not
an isolated `pg_dump`/`pg_restore` throughput number, physical `pg_upgrade`,
production downtime, RTO, or recovery assurance.

## Evidence and verification

Each series lives under `runs/operation-benchmarks/<series-id>` and contains:

- immutable operation and experiment spec snapshots with SHA-256 bindings;
- one exact linked ordinary experiment run per trial;
- a tree digest for every complete linked run;
- the standardized result reference, exact digest, and normalized value;
- individual trial JSON, aggregate statistics, CV gate, and regenerated human
  summary.

The producer also retains a closed snapshot of statically resolved experiment,
workload, profile, topology, config, and runner inputs and checks their digests
immediately before and after every trial. Trials still execute from the live
workspace. These boundary checks detect persistent mutation but do not prove
the absence of a concurrent transient change-and-revert during a trial; the
series therefore does not claim execution from the retained capsule.

Input discovery is deliberately bounded. Only the selected profile directory
may be traversed recursively; textual references must resolve to regular files.
The top-level runtime/output roots `.git`, `.tmp`, `generated`, `logs`,
`releases`, and `runs` are forbidden, as is the private `.env` file.
`.env.example` is an explicit fixed input. A series is refused if the retained
closure would exceed 1,024 files or 64 MiB in total. The verifier rebuilds the
closure from `inputs/` and rejects missing or undeclared files rather than
trusting the producer's file list.

Every linked experiment starts with an exact child environment. Ambient
workbench, experiment, workload, Compose, PostgreSQL, and shell-control
variables do not pass through. The only inherited process-bootstrap names are
`HOME`, `LOGNAME`, `PATH`, `TEMP`, `TMP`, `TMPDIR`, and `USER`; the runner fixes
`BASH_ENV=/dev/null`, `LANG=C`, `LC_ALL=C`, and `TZ=UTC`, then adds its own
run/pack/runtime identities. It also supplies `ENV_FILE=.env.example`, so an
operation trial cannot silently select a checkout-local `.env`. Native runs
add only the runner-inspected `PGWORKBENCH_NATIVE_BINDIR` and corresponding
toolchain digest. This is an environment-capability boundary, not an OS sandbox
or a claim that the invoked scenario code is harmless.

The producer resolves the running `pgworkbench` executable to an executable
regular file, records its SHA-256, and copies those exact bytes to
`protocol/engine/pgworkbench`. It rehashes the live executable before and after
every trial. Verification checks the retained copy against the recorded
digest. This proves only which engine bytes were retained and that the selected
file did not persistently change at those boundaries; it does not attest the
source commit, build process, dynamic libraries, or host.

For `runtime=native`, the same bounded identity mechanism covers exactly seven
PostgreSQL executables: `createdb`, `initdb`, `pg_ctl`, `pg_isready`, `pgbench`,
`postgres`, and `psql`. Their bytes, sizes, and observed version strings are
recorded under `protocol/native-toolchain/`, and the selected installation is
re-inspected before and after every trial. The retained directory is an
identity-only verification snapshot, not a relocatable PostgreSQL installation.
Its adjacent `share`/`lib` trees, extensions, dynamic/system libraries, source
commit, build flags, and build provenance remain `unattested`. Docker series
contain neither native identity fields nor a native toolchain snapshot.

The independent verifier reopens every linked run with the ordinary run
verifier, checks runtime/topology/experiment identity, recomputes its tree
digest, strictly reparses `operation-result.json`, recomputes every primary
value and aggregate, and rerenders the summary. Unknown fields, duplicate JSON
keys, non-finite or negative values, drifted snapshots, missing exact trials,
or changed result bytes fail verification.

The deterministic bundle contains the series and complete linked-run closure.
Its inventory records every relative path, size, and digest and is required by
`verify --bundle`. Bundle mode derives the artifact root from the canonical
`runs/operation-benchmarks/<series-id>` location inside the extracted tree; it
requires the inventory's `series_ref` to resolve to that exact directory and
does not fall back to linked runs from another checkout. Relocated verification
and result-byte tampering are covered by tests.

The inventory is self-contained and unsigned: it establishes internal closure
and detects changes relative to its own recorded hashes, but it does not
authenticate who produced the bundle. An actor able to replace all artifacts
can also regenerate this self-signed inventory. Publisher authenticity must
come from an external trusted archive digest, signature, or release provenance;
`verify --bundle` alone makes no such claim.

## Measurement bases

`operation-result` is preferred: the workload writes standardized JSON with a
precise metric and named clock/scope. `linked-run-wall-clock` is reserved for a
scenario that cannot emit a narrower result. Its value is re-derived from the
linked manifest start and terminal verdict finish and must be described as full
experiment wall-clock, including orchestration inside that interval.

Operation series do not enter `benchmark compare`, histories, or the AB/BA
decision path. A future comparison contract needs its own paired execution and
subject-isolation proof; two separately scheduled descriptive series are not
causal evidence.
