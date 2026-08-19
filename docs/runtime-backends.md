# Runtime Backends

Select the runtime through the CLI or environment:

```bash
pgworkbench experiment run --runtime docker smoke
pgworkbench experiment run --runtime native smoke
pgworkbench utility run --runtime native --run-id native-pgdump pg-dump/smoke
pgworkbench benchmark run --runtime native --subject baseline pgbench/smoke
pgworkbench benchmark operation run --runtime docker \
  massive-dml/offline-bulk-load-indexed
PGWORKBENCH_NATIVE_BINDIR=/absolute/postgresql/bin \
  pgworkbench benchmark operation run --runtime native \
  massive-dml/offline-bulk-load-indexed

PGWORKBENCH_RUNTIME=native make runtime-reset
PGWORKBENCH_RUNTIME=native make experiment-run EXPERIMENT_SPEC=smoke
```

## Docker

Docker Compose is the default and supports all declared topologies: `single`,
physical and logical replication, PgBouncer, and multi-version upgrade. Existing
`docker-*` targets remain compatibility aliases for the Docker backend.

Compose generates container names from the project and stable service names;
the former `POSTGRES_CONTAINER`, `POSTGRES_REPLICA_CONTAINER`,
`POSTGRES_LOGICAL_SUBSCRIBER_CONTAINER`, `PGBOUNCER_CONTAINER`,
`POSTGRES_UPGRADE_OLD_CONTAINER`, and `POSTGRES_UPGRADE_NEW_CONTAINER`
overrides are no longer part of the runtime configuration. Use
`docker compose ps -q postgres` (or another service name) when a container ID is
needed. Concurrent checkouts must resolve to distinct Compose project names and
host ports; different directory basenames can still collide after Compose name
normalization. Confirm each identity with
`docker compose --env-file .env.example config --format json | jq -r .name`,
then export the six assignments returned by `./scripts/assign_test_ports.sh`
before starting the second runtime.

## Native

Native mode uses host `initdb`, `pg_ctl`, `createdb`, `pg_isready`, and `psql`.
The managed backend creates and owns a private cluster beneath
`.tmp/native/<topology>/data`, binds only to a loopback address, uses the
configured disposable port/database, and refuses unsupported topologies. It
never adopts an existing cluster. `reset` removes only the resolved
workbench-owned runtime directory.

Required host commands:

```bash
pgworkbench doctor --runtime native
```

The shell layer requires Bash 4 or newer. On macOS, put a current Bash (for
example the Homebrew installation) ahead of the system Bash 3.2 on `PATH`.
`pgbench` is additionally required only for pgbench workload specs.
`pg_dump`, `pg_dumpall`, and `pg_restore` are required only when the matching
utility workload adapter is selected.

The intended standalone distribution is the release archive, which keeps the
binary beside the complete scenario pack. It discovers that pack from the
executable location, so neither the original checkout nor Go is required. If a
binary is deliberately installed elsewhere, set `PGWORKBENCH_ROOT` to the
pack root. A bare `go install` of only the executable is therefore not a
complete installation.

The native lifecycle helper understands `single` and `source-tree`, but the
first-class `experiment run` command and current release compatibility cells
support only the `single` topology. That managed `single` lifecycle also backs
the compatible utility, direct-PostgreSQL pgbench benchmark, both massive-DML
bulk-load operation, and manual-vacuum operation paths.
The digest-bound `pgbouncer` benchmark target is Docker-only and fails before
reserving benchmark evidence on native. Compose-only workload adapters such as
`compose-run` and noisia containers also fail with an explicit unsupported
error.

Every Docker pgbench measurement emits the local Compose service image ID
reported for the driver and measured target. The verifier derives those fields
from the retained linked transcript and includes them in the environment
population. This segments populations by the reported local identity; the
artifact does not retain or rehash image config/layer bytes and does not
authenticate publisher, registry origin, signature, or build provenance.

Every ordinary native benchmark resolves one concrete bindir (explicit
`--native-bindir`, `PGWORKBENCH_NATIVE_BINDIR`, `PG_INSTALL_DIR/bin`, or
`pg_config` discovery), snapshots the seven required PostgreSQL executables,
and revalidates them throughout the series. Docker is therefore optional for
the supported single-node benchmark path.

`benchmark ab-run --subject-dimension native_toolchain` selects two explicit
absolute trusted bindirs and starts workbench-owned native clusters with their
seven required executables. It binds and records only those executable bytes
and observed versions, not the surrounding installation or build provenance.

`benchmark driver-run` is a separate native external-process envelope, not the
managed backend lifecycle. It can address only a caller-acknowledged disposable
loopback, non-system database; the workbench does not create, own, or attest
that database. See [benchmarking.md](benchmarking.md#pinned-native-external-driver-execution).

`benchmark operation run --runtime native` also requires an absolute
`PGWORKBENCH_NATIVE_BINDIR`. Before reserving the series it inspects and
snapshots exactly `createdb`, `initdb`, `pg_ctl`, `pg_isready`, `pgbench`,
`postgres`, and `psql`, then revalidates that byte/version identity before and
after every trial. This is narrower than an installation or build-provenance
attestation. Docker operation series do not carry a native toolchain identity.

## Backend contract

Both backends expose `up`, `reset`, `restart`, `wait`, `status`, and `down` via
`scripts/runtime.sh`. Profile, dataset, workload, metrics, snapshot, assertion,
and evidence code talks to PostgreSQL through the same local connection
contract; it does not know how the server was started.

After `up` or `reset` succeeds, the experiment runner queries the live local
server for `server_version_num` and records the bounded runtime fingerprint in
`manifest.env` before applying configuration or running scenario steps. The
same capture path is used for Docker and native backends.

Podman, remote hosts, and Kubernetes can be added only by implementing this
contract plus safety, cleanup, compatibility, and release evidence. They are
not aliases for Docker and are not currently advertised.
