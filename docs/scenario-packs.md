# Scenario Packs

A scenario pack is the immutable input boundary for experiments. The built-in
pack is described by `pgworkbench-pack.json` and includes profiles, datasets,
workloads, experiments, topologies, configs, schemas, scripts, and guidance.

```bash
pgworkbench pack validate
pgworkbench pack inspect --json
pgworkbench pack export ./exported-pack
pgworkbench pack init --id my-postgres-lab ./my-postgres-lab
```

The `pgworkbench.scenario-pack/v1` manifest declares:

- a stable pack id and version;
- an engine version constraint;
- the exact asset roots included in inventory.

`engine_constraint` is an executable compatibility contract, not descriptive
metadata. It accepts one strict SemVer comparator with a complete
`major.minor.patch` version: `>=0.2.0`, `=0.2.1`, `^0.2.0`, or `~0.2.0`.
Prerelease and build identifiers follow SemVer 2.0.0 rules. Pack validation by
a release binary fails when its engine version does not satisfy the constraint
and reports two bounded choices: use a compatible engine, or migrate and retest
the pack before changing its constraint.

Source builds report engine compatibility as `unverified-development`; they do
not silently impersonate a compatible release. To test a future candidate from
a source checkout, make the candidate explicit:

```bash
go run ./cmd/pgworkbench pack validate --engine-version 0.3.0
```

The JSON inspection exposes `engine_compatibility.status`, its diagnostic, and
`release_evidence_eligible`. A compatible prerelease remains ineligible for
release evidence. Release snapshot export passes its exact candidate through
`--engine-version`; an explicit `dev` or `*-dev` value is rejected as a release
gate before the export destination is created.

Validation rejects unknown manifest fields, missing assets, symlinks,
path traversal, duplicate assets, and non-regular content. Inspection hashes
each file and produces one deterministic pack digest. Export requires an empty
destination, preserves executable bits, and revalidates the copied digest.
Experiment planning and execution also canonicalize the selected spec and
require its physical path to remain under the selected pack's `experiments/`
tree. Absolute external paths, `..`, and symlink escapes are rejected before a
pack identity can be attached to a run.

A pack-bound pgbench series retains the validated full file inventory at
`protocol/scenario-pack.json`, binds that exact inventory file and pack digest
from `result.json`, and revalidates the full live pack immediately before and
after every trial and again before finalization. A persistent change invalidates
the complete series. Trials still execute from the live pack root: these
boundary checks do not prove that a concurrent file was not changed and restored
entirely within one trial. The retained protocol capsule remains execution input
for the typed specs/config/workload SQL only; it is not a snapshot of all runtime
scripts and Compose assets.

Release archives contain the canonical, version-matched built-in pack next to
the binary. Candidate preflight requires the source pack version to equal the
release version; it never rewrites pack identity while packaging. The binary
discovers that pack from its executable directory, so `version`, planning,
validation, and execution do not depend on the original Git checkout.
`PGWORKBENCH_ROOT` selects an explicit pack when discovery is ambiguous.

`pack init` creates a complete, executable authoring starter by copying the
current pack into an empty directory and assigning a new id/version. Delete
unneeded profiles and scenarios from both the tree and its `assets` inventory,
then validate before sharing it. This deliberately starts from a working pack
instead of generating a partial directory that cannot execute on its own.

Pack identity does not make code trusted. SQL and shell hooks execute with the
operator's permissions; inspect and review third-party packs before running
them. Host-shell hooks require the explicit trusted-shell capability described
in [experiment-platform.md](experiment-platform.md); declarative SQL assertions
do not.
