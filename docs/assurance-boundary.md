# Assurance Boundary

A passed workbench result means that the recorded experiment completed its
workload, strict assertions, and failure scan against the recorded disposable
runtime, using the recorded scenario-pack and spec digests. Verification means
that the required artifact files and their recorded content remain internally
consistent.

The pgbench producer executes retained typed protocol inputs and also records
the complete configured scenario-pack inventory. It validates that full pack
before reservation, before and after every trial, and again before series
finalization. This detects persistent boundary drift and invalidates all
recorded trials, but it does not prove that a live pack file was not changed
and restored entirely inside one trial. Operation benchmarks likewise execute
from the live workspace: their retained static-input closure is checked before
and after each linked run, but a transient change-and-revert inside that window
remains outside the claim. Neither contract describes its live input tree as an
immutable execution sandbox.

An operation run narrows ambient process influence by starting each linked
experiment from a small runner-owned environment and explicitly selecting
`.env.example`; the private `.env` and runtime/output roots cannot enter its
bounded input closure. That closure is capped at 1,024 files and 64 MiB and is
independently rebuilt from the retained capsule. These controls do not isolate
filesystem, network, kernel, dynamic-library, or subprocess behavior and do not
make trusted scenario shell code safe to execute.

Operation evidence retains the exact `pgworkbench` executable bytes and, for a
native run, identity snapshots of seven selected PostgreSQL executables. The
live files are checked before and after each trial. This detects persistent
byte drift at those boundaries; it is not source/build provenance, dependency
closure, code signing, or host attestation. The native snapshot is not promised
to run after relocation.

A passed v1 run also records an observed producer OS/architecture, a named
fingerprint target, and that target's live PostgreSQL
`server_version_num`/derived major. `multi-version-upgrade` names only its
`upgrade-new` destination here; the field does not claim to identify the old
and new version pair. These values identify the bounded observation; they are
not host attestation, server authentication, performance evidence, or a
declaration that the tuple is supported. An early failed run may record the
fingerprint as unavailable when PostgreSQL never became queryable.

It does not prove:

- safety on a production database;
- representative throughput, latency, sizing, or a universal tuning choice;
- correctness for another PostgreSQL major, OS, architecture, topology, pack,
  dataset size, or runtime backend;
- absence of failures that the scenario did not observe;
- backup validity, restore completeness, PITR correctness, RPO, RTO, or SLA;
- authenticity of an unsigned artifact or honesty of the machine that produced
  it.

This authenticity limit applies specifically to operation bundles. Their
closed inventory is bound to the canonical series location and prevents a
verifier from borrowing linked runs from another tree, but the inventory is
self-signed and unsigned. Recomputing both content and inventory can create a
different internally consistent bundle; trust in the publisher requires an
external signature, trusted digest, or release provenance.

Shell hooks are trusted code from the selected scenario pack. Pack validation
provides deterministic identity and path safety; it is not a sandbox or a code
signature. Review untrusted packs before execution.

The native external-driver envelope is also not a sandbox. It invokes trusted
caller-selected files through an adapter-owned fixed argv and minimal
environment, bounds the process group and output, and rejects retained secret
bytes, symlinked required paths, and overwrite destinations. Execution contract
v2 first stages an adapter-selected runtime closure below `inputs/runtime/`,
normalizes its retained file modes, and binds a sorted path/digest/size/mode
inventory plus a canonical tree digest. Verification independently reconstructs
the BenchBase manifest/JAR/plugin closure, the exact one-file HammerDB launcher
root, or the sysbench executable/workload/common-Lua closure. The staged tree is
rehashed before and after execution and as part of relocated verification.

This is deliberately `driver_runtime_closure_attested=true`, not complete
runtime or provenance attestation. BenchBase still runs through an external
Java executable whose bytes are retained and checked before and after the run;
the JRE tree is not captured. HammerDB and sysbench execute their staged
retained launchers, but the dynamic loader, libc, libz, libpq, OpenSSL, libaio,
kernel, and other host dependencies remain ambient. The artifact therefore
requires `host_runtime_dependencies_attested=false`. It also does not prove
that any executable or JAR was built from the registry's pinned source, so
`source_to_binary_attested=false` remains mandatory.

Before execution the guard requires an explicit operator acknowledgement, an
exactly numeric-loopback config host, and a non-system database. BenchBase JDBC
target extraction is fail-closed. The acknowledgement and extracted endpoint
are integrity-bound and independently re-derived, but this proves neither
database identity nor ownership nor that the target is actually disposable or
non-production; there is deliberately no remote-target override. Nor does the
closure prove that schema preparation, scale, resource isolation, or workload
fairness was correct.

HammerDB's adapter-generated Tcl, exact `vurun` job-id binding, one saved public
report, and strict normalization are verified, but the public report does not
expose every generated execution setting and has no exhaustive error channel.
Password bytes are ephemeral and forbidden from retained evidence; this does
not turn any native process into a credential sandbox. Consequently the
artifact is a descriptive external single trial only; it cannot become a
pgbench series, A/B decision, TPC, or cross-system evidence. Protected-runner
provisioning and its additional limits are documented in
[external-driver-runner.md](external-driver-runner.md); that document is not
live-runner or release-gate evidence.

The native backend owns and manages a dedicated workbench `PGDATA`. The general
experiment runner intentionally does not accept an arbitrary external target.
It checks the primary and every topology-specific endpoint before creating a
run, requires loopback addresses, and rejects both non-local and system-database
override capabilities even when the generic utility guard would allow them.
Utilities that inspect external systems need a separate, explicit contract and
must not reuse the disposable-runtime safety claim.

An explicitly selected benchmark protocol v2 adds four independently
re-derived, run/protocol/trial-bound control records: PostgreSQL shared-buffer
residency for exact relations, runner-managed database/WAL statistics resets,
regular-grid PostgreSQL sampler duty-cycle calibration, and Docker
single-container CPU/memory enforcement. A satisfied record means only that
its bounded raw source, phase window, declared threshold, and derived status
agree. It does not establish OS page-cache state, a genuinely cold cache,
privileged-host honesty, dedicated hardware, absence of interference, or
resource enforcement outside the recorded container. The records are unsigned
and their SHA-256 digests provide content integrity, not attestation. Native
benchmark runs therefore support only the v2 `unbounded` resource mode;
runner-enforced budgets fail closed instead of being treated as declarations.
The normalized PostgreSQL sampler summary enforces an explicit maximum gap of
two declared intervals. In calibrated v2 mode, every regular metrics row must
also correlate with its exact monotonic-grid timing row; one untimed terminal
boundary row is allowed. A proven runner-managed reset may split only the
matching database or WAL counters at its recorded timestamp. The summed
segments omit unobservable work between the last pre-reset sample and reset,
so they are descriptive lower bounds rather than complete attribution.

Counterbalanced A/B protocol v3 additionally records only the `pg_settings`
rows named by the union of assignments in the two snapshotted configuration
files. The verifier re-parses that bounded raw source, binds it to the prepare
phase, and requires stable effective rows, one server version, no pending
restart, and at least one cross-arm `setting` plus `unit` difference. This
prevents comment-only, spelling-only, or unapplied configuration changes from
supporting a performance verdict. It is not proof that PostgreSQL consumed no
other configuration source, that an operator did not alter the server outside
the observed rows, or that the chosen setting caused every observed effect.
Likely credential-, connection-string-, secret-, token-, passphrase-, and
command-bearing names are refused, but the narrow collector is not a general
secret scanner.

The v3 `native_toolchain` A/B subject is narrower than its name may suggest.
It binds identity-only snapshots of exactly seven executable files and requires
matching observed version strings for all seven selected tools plus one
runtime `server_version_num` across both arms. It does not capture or attest
adjacent `share`/`lib` trees, extensions, dynamic or system libraries, build
flags, source commits, or build provenance, and therefore cannot establish
that a source patch was the sole causal difference. `source_commit` and
`build_provenance` deliberately remain `unattested`; any performance decision
is bounded to the selected seven-file executable byte sets and the recorded
runtime environment.

Selecting a native-toolchain bindir also authorizes pre-reservation execution
of the seven selected local files with `--version`. Each direct command has a
30-second context deadline, but this inspection is not sandboxing, code
signing, provenance attestation, or complete containment of arbitrary
descendant processes. Only trusted local installations should be selected.

Ordinary pgbench series retain the exact `pgworkbench` executable and bind its
digest into `environment.json`; the producer and verifier reject byte drift.
Every native series also retains a canonical series-local copy of the seven
selected PostgreSQL executables. A native-toolchain A/B run verifies its
arm-level protocol snapshot before creating that local copy and later requires
both closures to remain intact. These are identity-only byte sets with the
limits described above. Docker rows instead retain the local Compose image IDs
reported in the linked transcript as population-segmentation fields; image
config/layer bytes are not part of the artifact closure. None of these
mechanisms proves source commit/build provenance, registry origin, publisher
identity, signatures, or absence of runtime mutation below the observed
boundaries.

`pgdrill` has a different boundary: it orchestrates recovery through an existing
backup provider and can make bounded recovery-assurance claims. The workbench
bridge can export unsigned, immutable identity provenance from a verified
ordinary experiment, but it refuses benchmark runs and does not create a
pgdrill configuration or report. Its optional predicate remains
human-reviewed input that a future isolated consumer must validate again; the
baseline itself is not backup, restore, PITR, RPO, RTO, or SLA evidence.
