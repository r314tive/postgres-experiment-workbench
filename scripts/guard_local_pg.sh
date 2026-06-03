#!/usr/bin/env bash
set -euo pipefail

host="${POSTGRES_HOST:-127.0.0.1}"
db="${POSTGRES_DB:-pg_experiment_workbench}"
allow="${ALLOW_NONLOCAL_PG:-0}"

case "$host" in
  127.0.0.1|localhost|::1|postgres)
    ;;
  *)
    if [[ "$allow" != "1" ]]; then
      cat >&2 <<EOF
Refusing to target non-local PostgreSQL host: $host
Set ALLOW_NONLOCAL_PG=1 only when you intentionally want to run workbench
commands against a non-local disposable target.
EOF
      exit 2
    fi
    ;;
esac

case "$db" in
  postgres|template0|template1)
    if [[ "${ALLOW_SYSTEM_DB:-0}" != "1" ]]; then
      cat >&2 <<EOF
Refusing to target system database: $db
Set ALLOW_SYSTEM_DB=1 only for an explicit disposable-system-db experiment.
EOF
      exit 2
    fi
    ;;
esac
