# Hosted external-driver release smoke

`draft-external-drivers` is a protected, candidate-bound compatibility smoke on
GitHub's `ubuntu-24.04` hosted runner. It acquires the exact BenchBase,
HammerDB, sysbench, Java, and Maven inputs for that job, creates a fresh owned
PostgreSQL 16 cluster, prepares all three datasets, executes each advertised
adapter once, verifies the complete local execution artifacts, emits sanitized
metadata, and destroys every acquired runtime and database byte.

This is a release gate, not a performance benchmark. A passing job supports
only this bounded claim:

> The downloaded draft candidate executed and locally verified the three
> pinned adapter contracts against fresh loopback PostgreSQL 16 datasets in
> this hosted job.

It does not attest source-to-binary provenance, all host dependencies, dataset
equivalence outside this job, TPC compliance, benchmark comparability,
throughput, latency, capacity, or suitability for production. The repository
document is a design contract; only a successful protected tag run supplies
live evidence for an exact candidate.

## Trust and credential boundary

The job uses:

- `runs-on: ubuntu-24.04` and the protected `release-external-drivers`
  environment;
- only `actions: read`, `attestations: read`, and `contents: read` job
  permissions;
- the exact protected repository-control artifact produced earlier in the
  release graph;
- the Linux/amd64 binary downloaded from the authenticated draft release;
- runner-temporary acquisition, build, PostgreSQL, and full-execution roots.

The protected environment is an approval boundary, not a secret store for this
job. Repository setup must require at least one reviewer for
`release-external-drivers`, prevent self-review, and restrict deployment to the
release tag policy (for example selected `v*` tags). Merely naming the
environment in YAML does not configure those controls; verify them in repository
settings before a release. It supplies no driver passwords, runtime paths,
administration token, or ruleset-review variables.

The downloaded candidate, its provisioner, and every upstream build or dataset
preparer execute as a dedicated unprivileged user through an exact `env -i`
allowlist. GitHub command-file paths, `GH_TOKEN`, `GITHUB_TOKEN`, and Actions
artifact/cache service credentials are absent. The read-only token is scoped
only to workflow-authored authentication/download commands. Candidate files are
made read-only and their binary/helper/config digests are rechecked around
execution; the dedicated user can write only beneath its temporary sandbox.

PostgreSQL is initialized per job with host and local `trust` authentication,
listens only on `127.0.0.1:5432`, and is deleted after the job. There is no
long-lived database password and no password-bearing config, environment
secret, report, or retained metadata.

## Exact acquisition pins

[`scripts/provision_external_driver_gate.sh`](../scripts/provision_external_driver_gate.sh)
contains the production pins. The release path accepts no caller-supplied URL,
digest, root, or runtime path. Every archive must match its SHA-256, byte size,
archive format, and single top-level root before extraction.

| Input | Immutable identity | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| BenchBase source | commit `33c00473807ebd49304d114a6d769d2d2b2bbb34` | 43098598 | `804c9b3018f2f230f4ebbb5d0ebfed28ca417650037736f13fe9212d406fc4bc` |
| sysbench source | commit `ebf1c90da05dea94648165e4f149abc20c979557` | 1509951 | `2a664cb397ebb0678a91d7b876c1ffcebe728a52cbb1ffe0aa63b36fad1c9e1c` |
| HammerDB Ubuntu 24 distribution | release `v6.0` | 36188110 | `6e0b94724356f35f60760fdcacd0b19de655ec0477383d59e585a7235c4d4a58` |
| Temurin JDK | `23.0.2+7`, Linux x64 | 214525906 | `870ac8c05c6fe563e7a3878a47d0234b83c050e83651d2c47e8b822ec74512dd` |
| Temurin JRE | `23.0.2+7`, Linux x64 | 51939941 | `1a16c654e67a72dadfa632969a457404ad1cc30c6375857fdcb393e0592ce3ba` |
| Apache Maven | `3.8.4` binary ZIP | 9130223 | `ccd67d1ee4fd79339c9b6f95d1e5e1e0e0209a8c1b095d9291e009afa0a492a5` |

The HammerDB archive has an additional launcher check: `hammerdbcli` must be
11427109 bytes with SHA-256
`373dee97827a43c1598d7f49f157b7bd2baa10f28c0812d1e93f987c058b6ad4`.

Extraction rejects absolute paths, traversal, unexpected roots, special files,
and links except for contained links in the pinned Temurin archives and two
contained HammerDB `pylib/tclpy0.4.1` aliases. Link chains, cycles, escaping
targets, and child members beneath archive links are rejected; HammerDB links
do not enter the one-file curated runtime. Test-only
pin and fetcher overrides require `PGWORKBENCH_EXTERNAL_OFFLINE_TEST=1` and are
unconditionally rejected when `GITHUB_ACTIONS=true`.

Build-time Maven dependencies and Ubuntu packages are captured by observed
inventory/digests, not promoted to source-to-binary provenance. Consequently
`source_to_binary_attested` and
`host_runtime_dependencies_attested` remain `false`.

## Curated runtime roots

The helper builds or extracts into the runner-temporary state directory, then
creates the minimum adapter roots accepted by `--runtime-root`:

```text
runtimes/
├── benchbase/
│   ├── benchbase.jar
│   ├── config/plugin.xml
│   └── lib/*.jar
├── hammerdb/
│   └── hammerdbcli
└── sysbench/
    ├── bin/sysbench
    └── share/sysbench/
        ├── oltp_common.lua
        └── oltp_read_write.lua
```

The exact cardinalities are 28 BenchBase files, one HammerDB file, and three
sysbench files. BenchBase is built with the pinned JDK and Maven, then its
manifest-linked transitive JAR closure plus `config/plugin.xml` is selected.
HammerDB retains only the separately digest-checked launcher. sysbench is built
from the pinned commit with PostgreSQL support and retains its executable plus
the selected/common Lua files.

Every regular file receives a path, SHA-256, size, and numeric mode in
`runtime-set.json`; the canonical sorted inventory receives a tree digest. The
candidate adapter copies and verifies its selected closure locally. Full
runtime bytes, generated scripts, stdout/stderr, and execution directories stay
under runner-temporary roots and are never upload inputs.

## Fresh PostgreSQL 16 and bounded datasets

The hosted image's distro PostgreSQL service is stopped first. The workflow and
helper both reject a server already answering on `127.0.0.1:5432`. The helper
then runs PostgreSQL 16's `initdb` under its owned state root, sets loopback-only
listening, and applies disposable durability settings. These settings make the
cell unsuitable for performance measurement by design.

All datasets are prepared before execute-only adapter runs:

| Driver | Preparation contract | Postcondition |
| --- | --- | --- |
| BenchBase `33c0047` | TPC-C create/load, scale factor 1, loader seed 0, workload `randomSeed` 424242 | exactly one warehouse |
| HammerDB `v6.0` | PostgreSQL TPROC-C build, 20 warehouses, 4 build VUs | 20 warehouses and all nine required tables |
| sysbench `1.0.20` | `oltp_read_write.lua prepare`, seed 424242, one table, 10000 rows | exactly 10000 rows in `sbtest1` |

HammerDB schema preparation uses a small ephemeral command list limited to the
[`dbset`, `diset`, and `buildschema` CLI surface](https://www.hammerdb.com/docs/).
The exact setting names are checked against the pinned v6.0
[PostgreSQL TPROC-C schema builder](https://github.com/TPC-Council/HammerDB/blob/d33f879aec858063edd17aa2daa46db03abb2bae/scripts/tcl/postgres/tprocc/pg_tprocc_buildschema.tcl).
The helper does not retain that command list in the repository or upload it.
The adapter's one-line execute-only marker is likewise created only in runner
state; adapter-generated Tcl remains inside the deleted full execution
directory.

The release-only execution configs are closed and validated by the helper:

- BenchBase: one terminal, scale factor 1, 15 seconds, rate 100, standard
  `45,43,4,4,4` transaction weights, workload `randomSeed=424242`, and the
  pinned commit's [loader RNG seed 0](https://github.com/cmu-db/benchbase/blob/33c00473807ebd49304d114a6d769d2d2b2bbb34/src/main/java/com/oltpbenchmark/benchmarks/tpcc/TPCCUtil.java). Upstream rate pacing uses wall-clock scheduling and
  `Math.random`, so this is fixed-input bounded smoke, not a byte-identical or
  result-identical execution claim;
- HammerDB TPROC-C: 20 warehouses, 4 VUs, 1-minute ramp-up, 2-minute duration,
  1000000 total iterations;
- sysbench: 4 threads, 60 seconds, one 10000-row table, deterministic seed
  `424242`.

The existing 100-warehouse/32-VU HammerDB config remains available for manual
studies. It is deliberately not used by the release gate and remains
descriptive and performance-unqualified unless a separate benchmark protocol
closes the host and comparison controls.

## Execution and local verification

The workflow runs all three adapters through the downloaded candidate using
fixed `--runtime-root`, `--binary`, `--config`, `--script`, and `--workload`
inputs. Each call includes
`--acknowledge-external-disposable-target` and targets a distinct non-system
loopback database.

The complete v2 execution directory is immediately checked with
`benchmark driver-run-verify --json`. Verification requires, among other
things, completed status, exact driver ID, closed runtime inventory, matching
pre/post runtime tree, bounded target acknowledgement, and the negative
assurance claims. The producer also compares the staged runtime inventory with
the helper's source runtime inventory.

Only after those local checks pass does the workflow create one project-authored
JSON summary per driver. A downstream read-only job rehashes and validates
these summaries and the candidate/registry identity. It intentionally cannot
rerun `driver-run-verify`: the full execution directory and third-party runtime
bytes have already been destroyed and must not be conveyed through Actions.

## License and artifact boundary

The dependency review identified conveyance obligations for the acquired
third-party programs and their embedded/runtime components. The metadata records
BenchBase as `GPL-3.0-or-later AND Apache-2.0`, HammerDB as
`GPL-3.0-or-later`, and sysbench as `GPL-2.0-or-later`, while explicitly leaving
the complete dependency-license and source closure unattested. This is a
technical compliance boundary for this project, not legal advice. The
conservative release policy is absolute: no BenchBase JAR/library, HammerDB
launcher/Tcl/runtime member, sysbench binary/Lua file, JDK/JRE/Maven byte,
source archive, build tree, package cache, database, or full execution
directory may enter a success or failure Actions artifact or a public release
asset.

The successful artifact contains `metadata-only.tar.gz`, its checksum, and
exactly these nine JSON members inside the archive:

```text
executions/benchbase-postgresql-33c0047.json
executions/hammerdb-postgresql-6.0.json
executions/sysbench-postgresql-1.0.20.json
gate.json
provisioning/acquisitions.json
provisioning/datasets.json
provisioning/host.json
provisioning/runtime-set.json
repository-controls/repository-controls.json
```

The packager compares this list byte-for-byte with the actual file list,
requires regular bounded JSON files, and explicitly rejects typical runtime
extensions and entrypoint names. `gate.json` binds the candidate, workflow
attempt, draft archive, driver registry, protected controls, all four
provisioning documents, and all three sanitized execution summaries. Its
assurance block fixes:

```text
purpose=adapter-compatibility-release-smoke
artifact_payload=metadata-only-no-third-party-runtime-bytes
third_party_runtime_bytes_uploaded=false
performance_claim=false
benchmark_comparability_claim=false
binary_distributed_by_project=false
project_redistribution=false
runtime_replay_available=false
complete_license_or_source_closure_attested=false
source_to_binary_attested=false
host_runtime_dependencies_attested=false
```

On failure, the upload path is an exact one-file allowlist containing only the
workflow-authored `failure.json`; no candidate-produced provisioning document
is eligible. The state, work, logs, acquired archives, runtimes, generated
scripts, database, and whole evidence directory are never upload roots. The
`always()` cleanup step stops
the owned cluster, validates the state marker and narrow path prefix, deletes
the complete provisioning root, and deletes the separate execution root.

GitHub Actions retention is transport, not durable publication. If an operator
copies this evidence to a durable release-specific store, the copied object
must remain the exact metadata-only archive and checksum. The project does not
publish any upstream runtime bytes. Until a protected tag run and its retained
metadata exist, this document and local tests prove only the workflow contract,
not a completed live external-driver gate.

## Local fail-closed checks

Run:

```bash
./tests/external_driver_gate.sh
./tests/release_workflow_graph.sh
```

The offline provisioning test synthesizes local archives and a mock fetcher. It
checks valid acquisition, digest tampering, archive traversal, GitHub Actions
override rejection, marker-protected cleanup, exact built-in pins, and
release-config drift. It does not download or execute upstream programs and is
not substitute evidence for the protected hosted release smoke.
