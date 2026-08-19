# Scenario Authoring from a Release Archive

This path needs neither a Git checkout nor a Go toolchain. It uses the released
`pgworkbench` binary, a reviewed scenario pack, and a disposable native
PostgreSQL installation.

## 1. Extract and inspect the release

```bash
release_version="${PGWORKBENCH_RELEASE_VERSION:?export PGWORKBENCH_RELEASE_VERSION}"
tar -xzf "pgworkbench-${release_version}-darwin-arm64.tar.gz"
cd "pgworkbench-${release_version}-darwin-arm64"

export PGWORKBENCH_BIN="$PWD/pgworkbench"
"$PGWORKBENCH_BIN" version
"$PGWORKBENCH_BIN" pack validate
"$PGWORKBENCH_BIN" compatibility show
```

Use a runtime-gated archive matching the host OS and architecture for this
native tutorial. The current ledger runtime-gates `darwin/arm64` and
`linux/amd64`; `darwin/amd64` and `linux/arm64` are compile/package-only and
must not be inferred to support this execution path. Native execution also
requires PostgreSQL server and client binaries; point the workbench at them
when they are not already on `PATH`:

```bash
export PGWORKBENCH_NATIVE_BINDIR=/path/to/postgresql/bin
"$PGWORKBENCH_BIN" doctor --runtime native
```

## 2. Create an editable pack

```bash
"$PGWORKBENCH_BIN" pack init \
  --id my-postgres-lab \
  --version 0.1.0 \
  ../my-postgres-lab

cd ../my-postgres-lab
export PGWORKBENCH_ROOT="$PWD"
"$PGWORKBENCH_BIN" pack validate
```

`pack init` copies a complete executable starter rather than a fragment. Keep
the original released binary in `PGWORKBENCH_BIN`; `PGWORKBENCH_ROOT` tells it
to use the edited pack.

## 3. Make one bounded scenario change

Start by copying `experiments/smoke.env` to a new name. Change its name and one
declarative assertion, while keeping the `single` topology and `small` profile:

```bash
cp experiments/smoke.env experiments/my-smoke.env
```

For example, the new spec may keep the smoke setup and require exactly 10,000
rows:

```text
EXPERIMENT_NAME="my smoke"
EXPERIMENT_TOPOLOGY=single
EXPERIMENT_PROFILE=smoke
EXPERIMENT_PROFILE_SIZE=small
EXPERIMENT_PROFILE_SETUP=1
EXPERIMENT_ASSERT_TRUE_SQL="SELECT count(*) = 10000 FROM smoke.items;"
EXPERIMENT_METRICS=0
EXPERIMENT_SNAPSHOT=0
```

Review every host-shell hook before running a third-party pack. Pack validation
checks identity and path safety; it is not a sandbox.

## 4. Validate, plan, and execute

```bash
"$PGWORKBENCH_BIN" spec validate experiment my-smoke
"$PGWORKBENCH_BIN" experiment plan --expanded my-smoke

"$PGWORKBENCH_BIN" experiment run \
  --runtime native \
  --run-id authoring-smoke \
  my-smoke

"$PGWORKBENCH_BIN" run verify runs/authoring-smoke
```

The native backend creates only a workbench-owned cluster beneath
`.tmp/native/single`; it will not adopt an arbitrary PostgreSQL server.

## 5. Bundle and independently re-verify

```bash
"$PGWORKBENCH_BIN" run bundle \
  runs/authoring-smoke \
  generated/authoring-smoke.tar.gz

VERIFY_DIR="$(mktemp -d)"
tar -xzf generated/authoring-smoke.tar.gz -C "$VERIFY_DIR"
"$PGWORKBENCH_BIN" run verify --bundle "$VERIFY_DIR/authoring-smoke"
```

A successful required-inventory verification proves internal consistency and
completeness of this recorded run bundle.
It does not make the workload representative, authenticate an unsigned bundle,
or establish safety for production. See [assurance-boundary.md](assurance-boundary.md).

## Acceptance checklist

- `pack validate` reports the intended pack id, version, and digest.
- The expanded plan contains only reviewed workloads and hooks.
- The run has one terminal verdict and `run verify` passes.
- The extracted bundle passes `run verify --bundle` at a different absolute
  path.
- The PostgreSQL runtime is stopped with
  `PGWORKBENCH_RUNTIME=native scripts/runtime.sh down single` when finished.
