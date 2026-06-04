# Patchsets

Patchsets are optional inputs for PostgreSQL source-tree workloads.

Recommended layout:

```text
patchsets/
  <name>/
    <pg-ref>/
      patchset.env
      0001-description.patch
      0002-description.patch
      series
```

Catalog commands:

```bash
make patchset-list
make patchset-show PATCHSET=chaos/master
make patchset-validate
```

The Make targets use the Go CLI:

```bash
go run ./cmd/pgworkbench patchset list
go run ./cmd/pgworkbench patchset show chaos/master
go run ./cmd/pgworkbench patchset validate
```

Run a plan with a named patchset:

```bash
PG_PATCHSET=chaos/master make source-plan SOURCE_WORKLOAD_SPEC=pg-source/check
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master \
make workload-run WORKLOAD_SPEC=pg-source/check
```

Run with an ad hoc patch directory:

```bash
PG_PATCH_DIR=patchsets/<name>/<pg-ref> \
make workload-run WORKLOAD_SPEC=pg-source/check
```

`patchset.env` fields:

```bash
PATCHSET_NAME="<name>/<pg-ref>"
PATCHSET_DESCRIPTION="short purpose"
PATCHSET_PG_REF="master"
PATCHSET_FILES=""
PATCHSET_ALLOW_EMPTY="0"
PATCHSET_CONFIGURE_ARGS=""
PATCHSET_BUILD_CFLAGS=""
PATCHSET_TEST_INITDB_EXTRA_OPTS=""
```

Patch order is resolved from `PATCHSET_FILES`, then from a `series` file, then
from lexicographic `.patch`/`.diff` filenames. Keep patchsets small, named, and
explicit about the PostgreSQL ref they target. Do not vendor large external
patch suites into the generic workbench unless they are maintained as part of a
clear local experiment.
