# External Workload Templates

These specs are opt-in adapters for third-party workload and fuzzing tools.

They intentionally do not vendor or pin the external projects. Set the image
and command that match your local packaging, then run through the generic
`compose-run` adapter:

```bash
SQLANCER_IMAGE=your/sqlancer:tag \
SQLANCER_COMMAND='sqlancer ... postgres --host "$PGHOST" --port "$PGPORT"' \
  make workload-run WORKLOAD_SPEC=external/sqlancer-postgres
```

Every external workload container receives:

```text
PGHOST=postgres
PGPORT=5432
PGDATABASE
PGUSER
PGPASSWORD
DATABASE_URL
```

Use these templates for disposable local databases only.
