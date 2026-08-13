#!/usr/bin/env bash

# Protocol-v2 runtime controls. This file is sourced only by the experiment and
# pgbench adapters; each function is a no-op for the v1 contract.

benchmark_control_v2_active() {
  [[ "${PGWORKBENCH_BENCHMARK_CONTRACT_VERSION:-1}" = "2" ]]
}

benchmark_control_directory() {
  local control_run_dir="${PGWORKBENCH_BENCHMARK_CONTROL_RUN_DIR:-${RUN_DIR:-}}"
  [[ -n "$control_run_dir" ]] || return 2
  printf '%s\n' "$control_run_dir/artifacts/benchmark/controls"
}

benchmark_control_file_is_new() {
  local path="${1:?control evidence path is required}"
  [[ "$path" = "$(benchmark_control_directory)"/* && ! -e "$path" && ! -L "$path" ]]
}

benchmark_control_prepare_directory() {
  local directory
  benchmark_control_v2_active || return 0
  directory="$(benchmark_control_directory)"
  if [[ -L "$directory" || ! -d "$directory" ]]; then
    echo "Benchmark control directory must be a pre-created non-symlink directory: $directory" >&2
    return 2
  fi
}

benchmark_control_header_only() {
  local path="${1:?control evidence path is required}"
  local header="${2:?control evidence header is required}"
  benchmark_control_file_is_new "$path" || {
    echo "Refusing to overwrite or escape benchmark control evidence: $path" >&2
    return 2
  }
  ( set -o noclobber; umask 077; printf '%s\n' "$header" > "$path" )
}

benchmark_control_reset_timestamp() {
  local scope="${1:?statistics scope is required}"
  local sql output
  case "$scope" in
    current-database)
      sql="SELECT COALESCE(to_char(stats_reset AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'), 'null') FROM pg_catalog.pg_stat_database WHERE datname = current_database()"
      ;;
    cluster-wal)
      sql="SELECT COALESCE(to_char(stats_reset AT TIME ZONE 'UTC', 'YYYY-MM-DD\"T\"HH24:MI:SS.US\"Z\"'), 'null') FROM pg_catalog.pg_stat_wal"
      ;;
    *) return 2 ;;
  esac
  output="$("$REPO_DIR/scripts/psql.sh" -Atq -c "$sql")" || return
  [[ "$output" = "null" || "$output" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{6}Z$ ]] || {
    echo "Unexpected $scope statistics reset timestamp: $output" >&2
    return 2
  }
  printf '%s\n' "$output"
}

benchmark_control_run_statistics_reset() {
  local boundary="${1:?statistics reset boundary is required}"
  local configured="${PGWORKBENCH_BENCHMARK_STATISTICS_RESET_BOUNDARY:-none}"
  local policy="${PGWORKBENCH_BENCHMARK_STATISTICS_RESET_POLICY:-none}"
  local path temporary db_before db_after wal_before wal_after
  local db_rows=0 db_completed=false wal_rows=0 wal_completed=false status=0

  benchmark_control_v2_active || return 0
  [[ "$configured" = "$boundary" ]] || return 0
  [[ "$policy" = "runner-managed" ]] || {
    echo "Statistics reset boundary requires runner-managed policy" >&2
    return 2
  }
  path="$(benchmark_control_directory)/statistics-reset.tsv"
  benchmark_control_file_is_new "$path" || {
    echo "Statistics reset control already executed or escaped its owned path" >&2
    return 2
  }
  db_before="$(benchmark_control_reset_timestamp current-database)" || return
  wal_before="$(benchmark_control_reset_timestamp cluster-wal)" || return
  if "$REPO_DIR/scripts/psql.sh" -Atq -c "SELECT pg_catalog.pg_stat_reset()" >/dev/null; then
    db_rows=1
    db_completed=true
  else
    status=1
  fi
  if "$REPO_DIR/scripts/psql.sh" -Atq -c "SELECT pg_catalog.pg_stat_reset_shared('wal')" >/dev/null; then
    wal_rows=1
    wal_completed=true
  else
    status=1
  fi
  db_after="$(benchmark_control_reset_timestamp current-database)" || {
    db_after=unavailable
    status=1
  }
  wal_after="$(benchmark_control_reset_timestamp cluster-wal)" || {
    wal_after=unavailable
    status=1
  }
  temporary="$(mktemp "$(benchmark_control_directory)/.statistics-reset.XXXXXX")" || return
  (
    umask 077
    {
      printf 'record\tscope\tvalue\trows\tcommand_completed\n'
      printf 'timestamp-before\tcurrent-database\t%s\t\t\n' "$db_before"
      printf 'timestamp-after\tcurrent-database\t%s\t\t\n' "$db_after"
      printf 'timestamp-before\tcluster-wal\t%s\t\t\n' "$wal_before"
      printf 'timestamp-after\tcluster-wal\t%s\t\t\n' "$wal_after"
      printf 'operation\tcurrent-database\tpg_catalog.pg_stat_reset\t%s\t%s\n' "$db_rows" "$db_completed"
      printf "operation\tcluster-wal\tpg_catalog.pg_stat_reset_shared('wal')\t%s\t%s\n" "$wal_rows" "$wal_completed"
    } > "$temporary"
  ) || {
    rm -f -- "$temporary"
    return 1
  }
  chmod 0600 "$temporary"
  # A same-directory hard link publishes with O_EXCL semantics: a concurrent
  # file appearing after the initial check is never overwritten.
  if ! ln "$temporary" "$path"; then
    rm -f -- "$temporary"
    return 1
  fi
  rm -f -- "$temporary"
  return "$status"
}

benchmark_control_prepare_statistics_none() {
  local path
  benchmark_control_v2_active || return 0
  [[ "${PGWORKBENCH_BENCHMARK_STATISTICS_RESET_POLICY:-none}" = "none" ]] || return 0
  path="$(benchmark_control_directory)/statistics-reset.tsv"
  benchmark_control_header_only "$path" $'record\tscope\tvalue\trows\tcommand_completed'
}

benchmark_control_quote_relation_literals() {
  local relation
  local delimiter=""
  for relation in ${PGWORKBENCH_BENCHMARK_CACHE_TARGET_RELATIONS:-}; do
    [[ "$relation" =~ ^[A-Za-z_][A-Za-z0-9_$]*\.[A-Za-z_][A-Za-z0-9_$]*$ ]] || {
      echo "Invalid benchmark cache target relation: $relation" >&2
      return 2
    }
    printf "%s'%s'" "$delimiter" "$relation"
    delimiter=,
  done
}

benchmark_control_capture_cache() {
  local mode="${PGWORKBENCH_BENCHMARK_CACHE_REGIME:-uncontrolled}"
  local path targets sql
  benchmark_control_v2_active || return 0
  path="$(benchmark_control_directory)/cache-state.tsv"
  if [[ "$mode" = "uncontrolled" ]]; then
    benchmark_control_header_only "$path" $'relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks'
    return
  fi
  [[ "$mode" = "postgres-shared-buffer-warm" ]] || {
    echo "Unsupported benchmark cache control mode: $mode" >&2
    return 2
  }
  benchmark_control_file_is_new "$path" || {
    echo "Cache control already captured or escaped its owned path" >&2
    return 2
  }
  targets="$(benchmark_control_quote_relation_literals)" || return
  [[ -n "$targets" ]] || {
    echo "Warm cache control requires target relations" >&2
    return 2
  }
  # pg_buffercache is PostgreSQL-owned instrumentation. The OS page cache is
  # deliberately outside this control and remains uncontrolled.
  "$REPO_DIR/scripts/psql.sh" -Atq -c "CREATE EXTENSION IF NOT EXISTS pg_buffercache" >/dev/null
  sql="BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY;
  COPY (
    WITH database_identity AS (
      SELECT oid AS database_oid, dattablespace
      FROM pg_catalog.pg_database
      WHERE datname = pg_catalog.current_database()
    ), requested(relation) AS (
      SELECT unnest(ARRAY[$targets]::text[])
    ), targets AS (
      SELECT r.relation, d.database_oid, c.oid AS relation_oid,
             pg_catalog.pg_relation_filenode(c.oid) AS relation_filenode,
             COALESCE(NULLIF(c.reltablespace, 0), d.dattablespace) AS relation_tablespace,
             pg_catalog.pg_relation_size(c.oid, 'main') / pg_catalog.current_setting('block_size')::bigint AS relation_blocks
      FROM requested r
      CROSS JOIN database_identity d
      CROSS JOIN LATERAL (SELECT pg_catalog.to_regclass(r.relation)::oid AS oid) resolved
      JOIN pg_catalog.pg_class c ON c.oid = resolved.oid
    ), resident AS (
      SELECT b.reldatabase AS database_oid, b.reltablespace AS relation_tablespace,
             b.relfilenode AS relation_filenode, count(*)::bigint AS resident_blocks
      FROM public.pg_buffercache b
      WHERE b.relforknumber = 0
      GROUP BY b.reldatabase, b.reltablespace, b.relfilenode
    )
    SELECT t.relation, t.database_oid, t.relation_oid, 'main', t.relation_blocks,
           COALESCE(r.resident_blocks, 0)
    FROM targets t
    LEFT JOIN resident r
      ON r.database_oid = t.database_oid
     AND r.relation_tablespace = t.relation_tablespace
     AND r.relation_filenode = t.relation_filenode
    ORDER BY t.relation
  ) TO STDOUT;
  COMMIT;"
  (
    set -o noclobber
    umask 077
    printf 'relation\tdatabase_oid\trelation_oid\tfork\trelation_blocks\tresident_blocks\n' > "$path"
    "$REPO_DIR/scripts/psql.sh" -Atq -c "$sql" >> "$path"
  )
}

benchmark_control_resource_source_unbounded() {
  local path
  benchmark_control_v2_active || return 0
  [[ "${PGWORKBENCH_BENCHMARK_RESOURCE_BUDGET_MODE:-unbounded}" = "unbounded" ]] || return 0
  path="$(benchmark_control_directory)/resource-budget-source.json"
  benchmark_control_file_is_new "$path" || return 2
  ( set -o noclobber; umask 077; printf '{\n  "mode": "unbounded"\n}\n' > "$path" )
}

benchmark_control_enforce_resource_budget() {
  local mode="${PGWORKBENCH_BENCHMARK_RESOURCE_BUDGET_MODE:-unbounded}"
  local runtime="${PGWORKBENCH_RUNTIME:-docker}"
  local cpu_millicores="${PGWORKBENCH_BENCHMARK_CPU_BUDGET_MILLICORES:-}"
  local memory_mib="${PGWORKBENCH_BENCHMARK_MEMORY_BUDGET_MIB:-}"
  local container_id inspect nano_cpu memory_bytes cgroup_version id_digest path
  local -a compose_command=() compose_args=()

  benchmark_control_v2_active || return 0
  if [[ "$mode" = "unbounded" ]]; then
    benchmark_control_resource_source_unbounded
    return
  fi
  [[ "$mode" = "runner-enforced" && "$runtime" = "docker" ]] || {
    echo "Runner-enforced resource budgets require the Docker single-container adapter; runtime=$runtime" >&2
    return 2
  }
  [[ "${EXPERIMENT_TOPOLOGY:-single}" = "single" && "${PGWORKBENCH_BENCHMARK_RESOURCE_PROVIDER:-}" = "docker-single-container-linux-cgroup-v2" ]] || {
    echo "Runner-enforced resource budget requires exact Docker single-container provider and topology" >&2
    return 2
  }
  [[ "$cpu_millicores" =~ ^[1-9][0-9]*$ && "$memory_mib" =~ ^[1-9][0-9]*$ ]] || {
    echo "Runner-enforced resource budget requires positive integer CPU millicores and memory MiB" >&2
    return 2
  }
  read -r -a compose_command <<< "${COMPOSE:-docker compose}"
  if [[ -n "${ENV_PATH:-}" && -f "$ENV_PATH" ]]; then
    compose_args+=(--env-file "$ENV_PATH")
  fi
  container_id="$("${compose_command[@]}" "${compose_args[@]}" ps -q postgres)" || return
  [[ "$container_id" =~ ^[0-9a-f]{64}$ ]] || {
    echo "Docker Compose did not resolve one canonical postgres container id" >&2
    return 2
  }
  docker update --cpus "$(awk -v value="$cpu_millicores" 'BEGIN { printf "%.3f", value / 1000 }')" --memory "${memory_mib}m" "$container_id" >/dev/null
  inspect="$(docker inspect --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}}' "$container_id")" || return
  read -r nano_cpu memory_bytes <<< "$inspect"
  [[ "$nano_cpu" =~ ^[0-9]+$ && "$memory_bytes" =~ ^[0-9]+$ ]] || {
    echo "Docker inspect returned non-canonical resource limits" >&2
    return 2
  }
  cgroup_version="$(docker exec "$container_id" sh -c 'if [ -f /sys/fs/cgroup/cgroup.controllers ]; then printf 2; else printf 1; fi')" || return
  [[ "$cgroup_version" = "2" ]] || {
    echo "Runner-enforced resource budget requires cgroup v2 inside the postgres container" >&2
    return 2
  }
  id_digest="$(printf '%s' "$container_id" | { if command -v shasum >/dev/null 2>&1; then shasum -a 256; else sha256sum; fi; } | awk '{print "sha256:" $1}')"
  path="$(benchmark_control_directory)/resource-budget-source.json"
  benchmark_control_file_is_new "$path" || return 2
  (
    set -o noclobber
    umask 077
    printf '{\n  "mode": "runner-enforced",\n  "observed_docker_nano_cpus": %s,\n  "observed_docker_memory_bytes": %s,\n  "cgroup_version": "2",\n  "postgres_container_id_digest": "%s",\n  "pgbench_container_id_digest": "%s"\n}\n' "$nano_cpu" "$memory_bytes" "$id_digest" "$id_digest" > "$path"
  )
}

benchmark_control_prepare_overhead_unquantified() {
  local path
  benchmark_control_v2_active || return 0
  [[ "${PGWORKBENCH_BENCHMARK_COLLECTOR_OVERHEAD_MODE:-included-unquantified}" = "included-unquantified" ]] || return 0
  path="$(benchmark_control_directory)/collector-overhead.tsv"
  benchmark_control_header_only "$path" $'sequence\tscheduled_at\tstarted_at\tfinished_at\tduration_ns\tstatus'
}
