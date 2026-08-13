# PostgreSQL Config Profiles

`benchmark-drivers/sysbench-postgresql.json` is the closed, non-secret native
sysbench execution input used by `pgworkbench benchmark driver-run`. Copy it,
review the target and workload controls, and keep passwords out of the file;
the runner accepts password material only through
`PGWORKBENCH_DRIVER_PASSWORD` and removes its ephemeral pgpass before publish.
All external-driver configs are loopback-only, deny PostgreSQL system
databases, and require the CLI's explicit disposable/non-production target
acknowledgement. The acknowledgement is recorded but is not verified ownership
or proof that the database is actually disposable.

The same directory contains closed HammerDB v6.0 TPROC-C and TPROC-H
PostgreSQL execute-only inputs. `--script` must be a private ephemeral file
containing only `pgworkbench.hammerdb-v6-execute-only-template/v1` plus a
newline. The marker is an adapter API token, not Tcl, and is intentionally not
shipped as a standalone config. `pgworkbench` generates the fixed Tcl sequence
and rejects caller-supplied HammerDB code. Password bytes exist only in the
isolated process environment and in-memory HammerDB dictionary; they must not
appear in retained inputs, stdout, stderr, or the public redacted report.

Config profiles are small `postgresql.conf` snippets applied with
`ALTER SYSTEM` against the disposable workbench instance.

`benchmark-native-toolchain` fixes one neutral, observable GUC for the native
toolchain A/B stability gate. It is benchmark protocol input, not production
tuning guidance.

Run:

```bash
make pg-config-apply PG_CONFIG=debug-logging
make docker-reset PG_CONFIG=wal-heavy
```

Profiles are intentionally local and disposable. Settings that require restart
are applied, then the PostgreSQL container is restarted.

`checkpoint-fsync-heavy` is a benchmark stress subject: it keeps durability on,
shrinks WAL/checkpoint bounds, and enables I/O timing. It is not a production
tuning recommendation.
