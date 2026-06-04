# chaos/master Patchset Slot

Default patchset metadata for PostgreSQL `master` chaos-style source checks.

This slot is intentionally allowed to be empty. Add ordered `.patch` or `.diff`
files here, or add a `series` file when a local experiment needs a maintained
patch order.

Run a plan without cloning PostgreSQL:

```bash
PG_SOURCE_ACTION=plan PG_PATCHSET=chaos/master make workload-run WORKLOAD_SPEC=pg-source/check
```
