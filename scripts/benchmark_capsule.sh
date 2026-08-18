#!/usr/bin/env bash

# This file is sourced by benchmark execution adapters. A capsule is a
# runner-created, digest-bound copy of every user-authored protocol input. The
# helpers deliberately expose no generic path override: callers resolve only
# canonical paths derived from the typed benchmark plan.

benchmark_capsule_active() {
  [[ -n "${PGWORKBENCH_BENCHMARK_CAPSULE_ROOT:-}" ]]
}

benchmark_capsule_sha256() {
  local file="${1:?capsule file is required}"
  local digest

  if command -v shasum >/dev/null 2>&1; then
    digest="$(shasum -a 256 -- "$file" | awk '{print $1}')"
  elif command -v sha256sum >/dev/null 2>&1; then
    digest="$(sha256sum -- "$file" | awk '{print $1}')"
  else
    echo "A SHA-256 implementation (shasum or sha256sum) is required" >&2
    return 2
  fi
  [[ "$digest" =~ ^[0-9a-f]{64}$ ]] || {
    echo "Failed to calculate a canonical SHA-256 digest for: $file" >&2
    return 2
  }
  printf 'sha256:%s\n' "$digest"
}

benchmark_capsule_validate_root() {
  local repo_root series_id expected_root capsule_root current component
  local -a components=(runs benchmarks)

  benchmark_capsule_active || return 1
  repo_root="$(realpath "$REPO_DIR")"
  series_id="${PGWORKBENCH_BENCHMARK_SERIES_ID:-}"
  if [[ -z "$series_id" || ${#series_id} -gt 200 || ! "$series_id" =~ ^[A-Za-z0-9_.-]+$ || "$series_id" = "." || "$series_id" = ".." ]]; then
    echo "Benchmark capsule series id is invalid: ${series_id:-<empty>}" >&2
    return 2
  fi
  components+=("$series_id" protocol capsule)
  current="$repo_root"
  for component in "${components[@]}"; do
    current="$current/$component"
    if [[ -L "$current" || ! -d "$current" ]]; then
      echo "Benchmark capsule path component is missing, symlinked, or not a directory: $current" >&2
      return 2
    fi
  done
  expected_root="$current"
  capsule_root="${PGWORKBENCH_BENCHMARK_CAPSULE_ROOT:-}"
  if [[ "$capsule_root" != /* || -L "$capsule_root" || ! -d "$capsule_root" ]]; then
    echo "Benchmark capsule root must be an absolute non-symlink directory" >&2
    return 2
  fi
  capsule_root="$(realpath "$capsule_root")"
  if [[ "$capsule_root" != "$expected_root" ]]; then
    echo "Benchmark capsule root escaped its owned series path: $capsule_root" >&2
    return 2
  fi
  printf '%s\n' "$capsule_root"
}

benchmark_capsule_resolve() {
  local relative="${1:?capsule relative path is required}"
  local expected_digest="${2:?capsule digest is required}"
  local capsule_root current component index resolved actual_digest
  local -a components=()

  if [[ "$relative" = /* || "$relative" == *\\* || "$relative" = */ || ! "$expected_digest" =~ ^sha256:[0-9a-f]{64}$ ]]; then
    echo "Benchmark capsule file capability is invalid: $relative" >&2
    return 2
  fi
  IFS='/' read -r -a components <<< "$relative"
  if (( ${#components[@]} == 0 )); then
    echo "Benchmark capsule path is empty" >&2
    return 2
  fi
  capsule_root="$(benchmark_capsule_validate_root)" || return
  current="$capsule_root"
  for ((index = 0; index < ${#components[@]}; index++)); do
    component="${components[$index]}"
    if [[ -z "$component" || "$component" = "." || "$component" = ".." || ! "$component" =~ ^[A-Za-z0-9._-]+$ ]]; then
      echo "Benchmark capsule path is not portable: $relative" >&2
      return 2
    fi
    current="$current/$component"
    if [[ -L "$current" ]]; then
      echo "Benchmark capsule path must not contain symlinks: $current" >&2
      return 2
    fi
    if (( index + 1 < ${#components[@]} )); then
      if [[ ! -d "$current" ]]; then
        echo "Benchmark capsule path component is not a directory: $current" >&2
        return 2
      fi
    elif [[ ! -f "$current" ]]; then
      echo "Benchmark capsule input is not a regular file: $current" >&2
      return 2
    fi
  done
  resolved="$(realpath "$current")"
  if [[ "$resolved" != "$capsule_root/$relative" ]]; then
    echo "Benchmark capsule input escaped its canonical path: $resolved" >&2
    return 2
  fi
  actual_digest="$(benchmark_capsule_sha256 "$resolved")" || return
  if [[ "$actual_digest" != "$expected_digest" ]]; then
    echo "Benchmark capsule input digest mismatch: $relative" >&2
    return 2
  fi
  printf '%s\n' "$resolved"
}
