#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/run_pg_source_check.sh [plan|run|scan]

Builds and tests a PostgreSQL source tree, optionally applying a local patchset
first. This is for testing PostgreSQL itself, extensions, or patch behavior; it
does not use the workbench Docker database.

Environment:
  PG_SOURCE_ACTION=run
  PG_REPO_URL=https://github.com/postgres/postgres.git
  PG_REF=master
  PG_SOURCE_DIR=generated/pg-source/<run-id>/src
  PG_PATCH_DIR=
  PG_CHECK_TARGET=check
  PG_MAKE_JOBS=<nproc/sysctl>
  PG_CLONE_DEPTH=1
  PG_CONFIGURE_ARGS="--enable-debug --enable-cassert --enable-tap-tests"
  PG_BUILD_CFLAGS="-O0 -g"
  PG_TEST_INITDB_EXTRA_OPTS=
  PG_SOURCE_KEEP_GOING=1

Actions:
  plan  Print resolved configuration and exit.
  run   Clone/build/test/scan.
  scan  Scan PG_SOURCE_DIR or PG_SOURCE_RUN_DIR only.
USAGE
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

timestamp() {
  date -u +%Y%m%d_%H%M%S
}

cpu_count() {
  if command -v nproc >/dev/null 2>&1; then
    nproc
  elif command -v sysctl >/dev/null 2>&1; then
    sysctl -n hw.ncpu
  else
    printf '2\n'
  fi
}

resolve_path() {
  local path="$1"
  if [[ -z "$path" ]]; then
    return 0
  fi
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
  else
    printf '%s/%s\n' "$REPO_DIR" "$path"
  fi
}

ACTION="${1:-${PG_SOURCE_ACTION:-run}}"
RUN_ID="${PG_SOURCE_RUN_ID:-pg-${PG_REF:-master}-$(timestamp)}"
PG_REPO_URL="${PG_REPO_URL:-https://github.com/postgres/postgres.git}"
PG_REF="${PG_REF:-master}"
PG_CLONE_DEPTH="${PG_CLONE_DEPTH:-1}"
PG_SOURCE_RUN_DIR="$(resolve_path "${PG_SOURCE_RUN_DIR:-generated/pg-source/$RUN_ID}")"
PG_SOURCE_DIR="$(resolve_path "${PG_SOURCE_DIR:-$PG_SOURCE_RUN_DIR/src}")"
PG_INSTALL_DIR="$(resolve_path "${PG_INSTALL_DIR:-$PG_SOURCE_RUN_DIR/install}")"
PG_ARTIFACT_DIR="$(resolve_path "${PG_ARTIFACT_DIR:-$PG_SOURCE_RUN_DIR/artifacts}")"
PG_PATCH_DIR="$(resolve_path "${PG_PATCH_DIR:-}")"
PG_CHECK_TARGET="${PG_CHECK_TARGET:-check}"
PG_MAKE_JOBS="${PG_MAKE_JOBS:-$(cpu_count)}"
PG_CONFIGURE_ARGS="${PG_CONFIGURE_ARGS:---enable-debug --enable-cassert --enable-tap-tests}"
PG_BUILD_CFLAGS="${PG_BUILD_CFLAGS:--O0 -g}"
PG_TEST_INITDB_EXTRA_OPTS="${PG_TEST_INITDB_EXTRA_OPTS:-}"
PG_SOURCE_KEEP_GOING="${PG_SOURCE_KEEP_GOING:-1}"

print_config() {
  cat <<CONFIG
PG_SOURCE_ACTION=$ACTION
PG_REPO_URL=$PG_REPO_URL
PG_REF=$PG_REF
PG_SOURCE_RUN_DIR=$PG_SOURCE_RUN_DIR
PG_SOURCE_DIR=$PG_SOURCE_DIR
PG_INSTALL_DIR=$PG_INSTALL_DIR
PG_ARTIFACT_DIR=$PG_ARTIFACT_DIR
PG_PATCH_DIR=$PG_PATCH_DIR
PG_CHECK_TARGET=$PG_CHECK_TARGET
PG_MAKE_JOBS=$PG_MAKE_JOBS
PG_CLONE_DEPTH=$PG_CLONE_DEPTH
PG_CONFIGURE_ARGS=$PG_CONFIGURE_ARGS
PG_BUILD_CFLAGS=$PG_BUILD_CFLAGS
PG_TEST_INITDB_EXTRA_OPTS=$PG_TEST_INITDB_EXTRA_OPTS
PG_SOURCE_KEEP_GOING=$PG_SOURCE_KEEP_GOING
CONFIG
}

apply_patches() {
  if [[ -z "$PG_PATCH_DIR" ]]; then
    echo "No PG_PATCH_DIR set; skipping patch application."
    return 0
  fi

  if [[ ! -d "$PG_PATCH_DIR" ]]; then
    echo "Patch directory not found: $PG_PATCH_DIR" >&2
    exit 2
  fi

  mapfile -t patches < <(find "$PG_PATCH_DIR" -maxdepth 1 -type f \( -name '*.patch' -o -name '*.diff' \) | sort)
  if (( ${#patches[@]} == 0 )); then
    echo "No .patch or .diff files found in $PG_PATCH_DIR; skipping."
    return 0
  fi

  for patch in "${patches[@]}"; do
    echo "Checking patch: $(basename "$patch")"
    git -C "$PG_SOURCE_DIR" apply --check "$patch"
    echo "Applying patch: $(basename "$patch")"
    git -C "$PG_SOURCE_DIR" apply "$patch"
  done
}

collect_artifacts() {
  mkdir -p "$PG_ARTIFACT_DIR/diffs" "$PG_ARTIFACT_DIR/logs" "$PG_ARTIFACT_DIR/cores"

  copy_under_artifact_dir() {
    local src="$1"
    local dest_root="$2"
    local rel="${src#"$PG_SOURCE_DIR"/}"
    mkdir -p "$dest_root/$(dirname "$rel")"
    cp "$src" "$dest_root/$rel"
  }

  while IFS= read -r -d '' file; do
    copy_under_artifact_dir "$file" "$PG_ARTIFACT_DIR/diffs"
  done < <(find "$PG_SOURCE_DIR" -name '*.diffs' -type f -print0 2>/dev/null)

  while IFS= read -r -d '' file; do
    copy_under_artifact_dir "$file" "$PG_ARTIFACT_DIR/logs"
  done < <(
    find "$PG_SOURCE_DIR" \( -name '*.log' -o -name '*.out' -o -name 'postmaster.log' -o -name 'regression.out' \) \
      -type f -print0 2>/dev/null
  )

  while IFS= read -r -d '' file; do
    copy_under_artifact_dir "$file" "$PG_ARTIFACT_DIR/cores"
  done < <(find "$PG_SOURCE_DIR" \( -name 'core' -o -name 'core.*' \) -type f -print0 2>/dev/null)
}

scan_artifacts() {
  "$REPO_DIR/scripts/scan_pg_failures.sh" "$PG_SOURCE_DIR" "$PG_ARTIFACT_DIR"
}

run_check() {
  mkdir -p "$PG_SOURCE_RUN_DIR" "$PG_ARTIFACT_DIR"

  if [[ -e "$PG_SOURCE_DIR" && ! -d "$PG_SOURCE_DIR/.git" ]]; then
    echo "PG_SOURCE_DIR exists but is not a git checkout: $PG_SOURCE_DIR" >&2
    exit 2
  fi

  if [[ ! -d "$PG_SOURCE_DIR/.git" ]]; then
    git clone --depth "$PG_CLONE_DEPTH" --branch "$PG_REF" "$PG_REPO_URL" "$PG_SOURCE_DIR"
  fi

  apply_patches

  (
    cd "$PG_SOURCE_DIR"
    read -r -a configure_args <<< "$PG_CONFIGURE_ARGS"
    ./configure --prefix="$PG_INSTALL_DIR" "${configure_args[@]}"

    make -j"$PG_MAKE_JOBS" CFLAGS="$PG_BUILD_CFLAGS"

    set +e
    read -r -a check_target_args <<< "$PG_CHECK_TARGET"
    local_log_name="${PG_CHECK_TARGET// /_}"
    if [[ -n "$PG_TEST_INITDB_EXTRA_OPTS" ]]; then
      make "${check_target_args[@]}" -j"$PG_MAKE_JOBS" PG_TEST_INITDB_EXTRA_OPTS="$PG_TEST_INITDB_EXTRA_OPTS" 2>&1 | tee "$PG_ARTIFACT_DIR/$local_log_name.log"
    else
      make "${check_target_args[@]}" -j"$PG_MAKE_JOBS" 2>&1 | tee "$PG_ARTIFACT_DIR/$local_log_name.log"
    fi
    CHECK_EXIT="${PIPESTATUS[0]}"
    set -e

    exit "$CHECK_EXIT"
  )
}

case "$ACTION" in
  plan)
    print_config
    ;;
  run)
    print_config
    set +e
    run_check
    CHECK_EXIT="$?"
    set -e

    collect_artifacts

    set +e
    scan_artifacts | tee "$PG_ARTIFACT_DIR/scan_pg_failures.log"
    SCAN_EXIT="${PIPESTATUS[0]}"
    set -e

    if [[ "$CHECK_EXIT" != "0" ]]; then
      echo "PostgreSQL source check target failed: exit=$CHECK_EXIT" >&2
    fi
    if [[ "$SCAN_EXIT" != "0" ]]; then
      echo "Failure evidence found in PostgreSQL artifacts." >&2
    fi

    if [[ "$PG_SOURCE_KEEP_GOING" = "1" ]]; then
      if [[ "$CHECK_EXIT" != "0" || "$SCAN_EXIT" != "0" ]]; then
        exit 1
      fi
    else
      exit "$CHECK_EXIT"
    fi
    ;;
  scan)
    print_config
    collect_artifacts
    scan_artifacts
    ;;
  *)
    usage >&2
    echo "Unknown action: $ACTION" >&2
    exit 2
    ;;
esac
