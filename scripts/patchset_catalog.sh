#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/patchset_catalog.sh list
  scripts/patchset_catalog.sh show <patchset>
  scripts/patchset_catalog.sh validate [patchset...]

Patchsets live under patchsets/<name>/<pg-ref>/patchset.env.
USAGE
}

list_patchsets() {
  find "$REPO_DIR/patchsets" -mindepth 2 -maxdepth 3 -type f -name patchset.env 2>/dev/null | sort | while read -r spec; do
    spec="${spec#"$REPO_DIR/patchsets/"}"
    printf '%s\n' "${spec%/patchset.env}"
  done
}

resolve_patchset_dir() {
  local patchset="${1:?patchset is required}"
  local dir="$REPO_DIR/patchsets/$patchset"

  if [[ -f "$dir/patchset.env" ]]; then
    printf '%s\n' "$dir"
    return 0
  fi

  echo "Patchset spec not found: $patchset" >&2
  exit 1
}

reset_metadata() {
  local patchset="$1"

  PATCHSET_NAME="$patchset"
  PATCHSET_DESCRIPTION=""
  PATCHSET_PG_REF=""
  PATCHSET_FILES=""
  PATCHSET_ALLOW_EMPTY="0"
  PATCHSET_CONFIGURE_ARGS=""
  PATCHSET_BUILD_CFLAGS=""
  PATCHSET_TEST_INITDB_EXTRA_OPTS=""
}

load_metadata() {
  local dir="$1"
  local patchset="$2"

  reset_metadata "$patchset"
  set -a
  # shellcheck disable=SC1090
  source "$dir/patchset.env"
  set +a
}

patch_entries() {
  local dir="$1"

  if [[ -n "${PATCHSET_FILES:-}" ]]; then
    printf '%s\n' $PATCHSET_FILES
    return 0
  fi

  if [[ -f "$dir/series" ]]; then
    sed -e 's/[[:space:]]*#.*$//' -e '/^[[:space:]]*$/d' "$dir/series"
    return 0
  fi

  find "$dir" -maxdepth 1 -type f \( -name '*.patch' -o -name '*.diff' \) | sort | while read -r file; do
    basename "$file"
  done
}

show_patchset() {
  local patchset="$1"
  local dir
  local entry

  dir="$(resolve_patchset_dir "$patchset")"
  load_metadata "$dir" "$patchset"

  printf 'PATCHSET_NAME="%s"\n' "$PATCHSET_NAME"
  printf 'PATCHSET_DESCRIPTION="%s"\n' "$PATCHSET_DESCRIPTION"
  printf 'PATCHSET_PG_REF="%s"\n' "$PATCHSET_PG_REF"
  printf 'PATCHSET_FILES="%s"\n' "$PATCHSET_FILES"
  printf 'PATCHSET_ALLOW_EMPTY="%s"\n' "$PATCHSET_ALLOW_EMPTY"
  printf 'PATCHSET_CONFIGURE_ARGS="%s"\n' "$PATCHSET_CONFIGURE_ARGS"
  printf 'PATCHSET_BUILD_CFLAGS="%s"\n' "$PATCHSET_BUILD_CFLAGS"
  printf 'PATCHSET_TEST_INITDB_EXTRA_OPTS="%s"\n' "$PATCHSET_TEST_INITDB_EXTRA_OPTS"
  printf 'PATCHSET_DIR="%s"\n' "$dir"
  printf 'PATCHSET_RESOLVED_FILES="'
  local first=1
  while IFS= read -r entry; do
    [[ -z "$entry" ]] && continue
    if (( first == 0 )); then
      printf ' '
    fi
    printf '%s' "$entry"
    first=0
  done < <(patch_entries "$dir")
  printf '"\n'
}

validate_entry() {
  local dir="$1"
  local entry="$2"

  if [[ "$entry" = /* || "$entry" = *..* ]]; then
    echo "Patch entries must be relative filenames under $dir: $entry" >&2
    return 1
  fi

  if [[ ! -f "$dir/$entry" ]]; then
    echo "Patch file not found: $dir/$entry" >&2
    return 1
  fi
}

validate_patchset() {
  local patchset="$1"
  local dir
  local entry
  local count=0
  local status=0

  dir="$(resolve_patchset_dir "$patchset")"
  load_metadata "$dir" "$patchset"

  [[ "$PATCHSET_NAME" = "$patchset" ]] || { echo "PATCHSET_NAME mismatch for $patchset: $PATCHSET_NAME" >&2; status=1; }
  [[ -n "$PATCHSET_DESCRIPTION" ]] || { echo "PATCHSET_DESCRIPTION is required for $patchset" >&2; status=1; }
  [[ -n "$PATCHSET_PG_REF" ]] || { echo "PATCHSET_PG_REF is required for $patchset" >&2; status=1; }

  while IFS= read -r entry; do
    [[ -z "$entry" ]] && continue
    count=$((count + 1))
    validate_entry "$dir" "$entry" || status=1
  done < <(patch_entries "$dir")

  if (( count == 0 )) && [[ "$PATCHSET_ALLOW_EMPTY" != "1" ]]; then
    echo "Patchset has no patch files and PATCHSET_ALLOW_EMPTY is not 1: $patchset" >&2
    status=1
  fi

  return "$status"
}

validate_patchsets() {
  local patchsets=("$@")
  local patchset
  local status=0

  if (( ${#patchsets[@]} == 0 )); then
    mapfile -t patchsets < <(list_patchsets)
  fi

  for patchset in "${patchsets[@]}"; do
    validate_patchset "$patchset" || status=1
  done

  if [[ "$status" = "0" ]]; then
    echo "PASS: patchset catalog"
  fi

  return "$status"
}

ACTION="${1:-help}"
if [[ $# -gt 0 ]]; then
  shift
fi

case "$ACTION" in
  help|-h|--help)
    usage
    ;;
  list)
    list_patchsets
    ;;
  show)
    show_patchset "${1:?patchset is required}"
    ;;
  validate)
    validate_patchsets "$@"
    ;;
  *)
    show_patchset "$ACTION"
    ;;
esac
