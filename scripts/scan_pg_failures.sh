#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
Usage:
  scripts/scan_pg_failures.sh [path ...]

Scans PostgreSQL test artifacts and logs for failure evidence:
  - core files
  - assertion failures and PANIC/FATAL crash lines
  - crash signals and postmaster crash messages
  - regression diff error lines
  - sanitizer and Valgrind error summaries

Environment:
  SCAN_CONTEXT_LINES=2

Exit code:
  0  no failure evidence found
  1  failure evidence found
USAGE
}

if [[ "${1:-}" = "-h" || "${1:-}" = "--help" ]]; then
  usage
  exit 0
fi

PATHS=("$@")
if (( ${#PATHS[@]} == 0 )); then
  PATHS=(logs generated)
fi

CONTEXT_LINES="${SCAN_CONTEXT_LINES:-2}"
FOUND=0
EXISTING_PATHS=()

for path in "${PATHS[@]}"; do
  if [[ -e "$path" ]]; then
    EXISTING_PATHS+=("$path")
  fi
done

section() {
  printf '\n== %s ==\n' "$1"
}

print_matches() {
  local label="$1"
  local pattern="$2"
  shift 2
  local files=("$@")
  local matched=0
  local file

  section "$label"
  for file in "${files[@]}"; do
    if grep -I -E -q "$pattern" "$file" 2>/dev/null; then
      matched=1
      FOUND=1
      printf -- '-- %s --\n' "$file"
      grep -I -E -n -C "$CONTEXT_LINES" "$pattern" "$file" | head -120 || true
    fi
  done

  if (( matched == 0 )); then
    printf 'clean\n'
  fi
}

if (( ${#EXISTING_PATHS[@]} == 0 )); then
  printf 'No scan paths exist: %s\n' "${PATHS[*]}"
  printf 'result=clean\n'
  exit 0
fi

mapfile -d '' ALL_FILES < <(find "${EXISTING_PATHS[@]}" -type f -print0 2>/dev/null)
mapfile -d '' LOG_FILES < <(
  find "${EXISTING_PATHS[@]}" \
    \( -name '*.log' \
       -o -name '*.out' \
       -o -name '*.diffs' \
       -o -name 'postmaster.log' \
       -o -name 'regression.out' \
       -o -path '*/tmp_check/*/log/*.log' \) \
    -type f -print0 2>/dev/null
)
mapfile -d '' DIFF_FILES < <(find "${EXISTING_PATHS[@]}" -name '*.diffs' -type f -print0 2>/dev/null)
mapfile -d '' CORE_FILES < <(find "${EXISTING_PATHS[@]}" \( -name 'core' -o -name 'core.*' \) -type f -print0 2>/dev/null)

section "core files"
if (( ${#CORE_FILES[@]} > 0 )); then
  FOUND=1
  printf '%s\n' "${CORE_FILES[@]}"
else
  printf 'clean\n'
fi

CRASH_PATTERN='TRAP:|PANIC:|server process .* was terminated by signal|terminating any other active server processes|segmentation fault|segfault|SIGSEGV|SIGBUS|SIGABRT|SIGILL|core dumped'
DIFF_PATTERN='^\+ERROR|unrecognized node type|server closed the connection unexpectedly|could not find pathkey item to sort'
SANITIZER_PATTERN='AddressSanitizer|UndefinedBehaviorSanitizer|LeakSanitizer|ThreadSanitizer|runtime error:'
VALGRIND_PATTERN='ERROR SUMMARY: [1-9][0-9]* errors|Invalid read|Invalid write|Use of uninitialised|Conditional jump or move depends on uninitialised'

print_matches "crash and assertion patterns" "$CRASH_PATTERN" "${LOG_FILES[@]}"
print_matches "regression diff error patterns" "$DIFF_PATTERN" "${DIFF_FILES[@]}"
print_matches "sanitizer patterns" "$SANITIZER_PATTERN" "${LOG_FILES[@]}"
print_matches "valgrind patterns" "$VALGRIND_PATTERN" "${LOG_FILES[@]}"

printf '\n== summary ==\n'
printf 'paths=%s\n' "${PATHS[*]}"
printf 'files_seen=%s\n' "${#ALL_FILES[@]}"
if (( FOUND == 1 )); then
  printf 'result=failure-evidence-found\n'
  exit 1
fi

printf 'result=clean\n'
