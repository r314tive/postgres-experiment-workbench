# Patchsets

Patchsets are optional inputs for PostgreSQL source-tree workloads.

Recommended layout:

```text
patchsets/
  <name>/
    <pg-ref>/
      0001-description.patch
      0002-description.patch
```

Run with:

```bash
PG_PATCH_DIR=patchsets/<name>/<pg-ref> \
make workload-run WORKLOAD_SPEC=pg-source/check
```

Patches are applied in lexicographic order. Keep patchsets small, named, and
explicit about the PostgreSQL ref they target. Do not vendor large external
patch suites into the generic workbench unless they are maintained as part of a
clear local experiment.
