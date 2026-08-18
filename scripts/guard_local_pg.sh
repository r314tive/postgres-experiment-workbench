#!/usr/bin/env bash
set -euo pipefail

host="${POSTGRES_HOST:-127.0.0.1}"
db="${POSTGRES_DB:-pg_experiment_workbench}"
allow="${ALLOW_NONLOCAL_PG:-0}"
experiment_mode="${PGWORKBENCH_EXPERIMENT_MODE:-0}"

case "$experiment_mode" in
  0|1)
    ;;
  *)
    echo "PGWORKBENCH_EXPERIMENT_MODE must be 0 or 1: $experiment_mode" >&2
    exit 2
    ;;
esac

if [[ "$experiment_mode" = "1" ]]; then
  if [[ "${ALLOW_NONLOCAL_PG:-0}" != "0" ]]; then
    echo "Experiment runs do not support ALLOW_NONLOCAL_PG; use a disposable loopback runtime" >&2
    exit 2
  fi
  if [[ "${ALLOW_SYSTEM_DB:-0}" != "0" ]]; then
    echo "Experiment runs do not support ALLOW_SYSTEM_DB; use a dedicated disposable database" >&2
    exit 2
  fi

  case "$db" in
    postgres|template0|template1)
      echo "Experiment runs refuse PostgreSQL system database: $db" >&2
      exit 2
      ;;
  esac

  for name in \
    POSTGRES_HOST \
    POSTGRES_REPLICA_HOST \
    POSTGRES_LOGICAL_SUBSCRIBER_HOST \
    POSTGRES_UPGRADE_OLD_HOST \
    POSTGRES_UPGRADE_NEW_HOST \
    PGBOUNCER_HOST; do
    value="${!name:-127.0.0.1}"
    case "$value" in
      127.0.0.1|localhost|::1)
        ;;
      *)
        echo "Experiment runs require loopback $name; refusing: $value" >&2
        exit 2
        ;;
    esac
  done

  exit 0
fi

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
