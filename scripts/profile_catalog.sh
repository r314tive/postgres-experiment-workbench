#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<'USAGE'
Usage:
  scripts/profile_catalog.sh list
  scripts/profile_catalog.sh show <profile>
  scripts/profile_catalog.sh validate [profile...]

Profiles may include optional profiles/<name>/profile.env metadata. Metadata
files are trusted local shell env files.
USAGE
}

list_profiles() {
  find "$REPO_DIR/profiles" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort | while read -r dir; do
    basename "$dir"
  done
}

resolve_profile_dir() {
  local profile="${1:?profile is required}"
  local dir="$REPO_DIR/profiles/$profile"

  if [[ -d "$dir" ]]; then
    printf '%s\n' "$dir"
    return 0
  fi

  echo "Profile not found: $profile" >&2
  exit 1
}

load_profile_metadata() {
  local dir="$1"
  local profile="$2"
  local meta="$dir/profile.env"

  PROFILE_NAME="$profile"
  PROFILE_DESCRIPTION=""
  PROFILE_TAGS=""
  PROFILE_SCHEMAS=""
  PROFILE_SIZES="small medium large"
  PROFILE_DEFAULT_SIZE="small"
  PROFILE_REQUIRES_TOPOLOGY="single"
  PROFILE_BACKGROUND_WORKLOADS=""
  PROFILE_DIAGNOSTIC_SQL=""

  if [[ -f "$meta" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$meta"
    set +a
  fi
}

show_profile() {
  local profile="$1"
  local dir

  dir="$(resolve_profile_dir "$profile")"
  load_profile_metadata "$dir" "$profile"

  printf 'PROFILE_NAME="%s"\n' "$PROFILE_NAME"
  printf 'PROFILE_DESCRIPTION="%s"\n' "$PROFILE_DESCRIPTION"
  printf 'PROFILE_TAGS="%s"\n' "$PROFILE_TAGS"
  printf 'PROFILE_SCHEMAS="%s"\n' "$PROFILE_SCHEMAS"
  printf 'PROFILE_SIZES="%s"\n' "$PROFILE_SIZES"
  printf 'PROFILE_DEFAULT_SIZE="%s"\n' "$PROFILE_DEFAULT_SIZE"
  printf 'PROFILE_REQUIRES_TOPOLOGY="%s"\n' "$PROFILE_REQUIRES_TOPOLOGY"
  printf 'PROFILE_BACKGROUND_WORKLOADS="%s"\n' "$PROFILE_BACKGROUND_WORKLOADS"
  printf 'PROFILE_DIAGNOSTIC_SQL="%s"\n' "$PROFILE_DIAGNOSTIC_SQL"
}

validate_profile() {
  local profile="$1"
  local dir meta
  local status=0

  dir="$(resolve_profile_dir "$profile")"
  meta="$dir/profile.env"

  [[ -f "$dir/README.md" ]] || { echo "Missing README.md for profile: $profile" >&2; status=1; }
  [[ -f "$dir/sql/00_setup.sql" ]] || { echo "Missing sql/00_setup.sql for profile: $profile" >&2; status=1; }
  [[ -f "$dir/sql/10_run.sql" ]] || { echo "Missing sql/10_run.sql for profile: $profile" >&2; status=1; }

  load_profile_metadata "$dir" "$profile"

  if [[ -f "$meta" ]]; then
    [[ "$PROFILE_NAME" = "$profile" ]] || { echo "PROFILE_NAME mismatch in $meta: $PROFILE_NAME" >&2; status=1; }
    [[ -n "$PROFILE_DESCRIPTION" ]] || { echo "PROFILE_DESCRIPTION is required in $meta" >&2; status=1; }
    if [[ " $PROFILE_SIZES " != *" $PROFILE_DEFAULT_SIZE "* ]]; then
      echo "PROFILE_DEFAULT_SIZE must be listed in PROFILE_SIZES for $profile" >&2
      status=1
    fi
  fi

  return "$status"
}

validate_profiles() {
  local profiles=("$@")
  local profile
  local status=0

  if (( ${#profiles[@]} == 0 )); then
    mapfile -t profiles < <(list_profiles)
  fi

  for profile in "${profiles[@]}"; do
    validate_profile "$profile" || status=1
  done

  if [[ "$status" = "0" ]]; then
    echo "PASS: profile catalog"
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
    list_profiles
    ;;
  show)
    show_profile "${1:?profile is required}"
    ;;
  validate)
    validate_profiles "$@"
    ;;
  *)
    show_profile "$ACTION"
    ;;
esac
