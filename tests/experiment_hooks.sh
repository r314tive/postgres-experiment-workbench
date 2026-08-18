#!/usr/bin/env bash
set -euo pipefail

export PGWORKBENCH_SUPERVISED=1
INTERNAL_RUN_ACTION=__pgworkbench_internal_run_v1

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$REPO_DIR/.tmp"
TMP_DIR="$(mktemp -d "$REPO_DIR/.tmp/experiment-hooks.XXXXXX")"
SPEC_DIR="$(mktemp -d "$REPO_DIR/experiments/.experiment-hooks.XXXXXX")"
trap 'rm -rf -- "$TMP_DIR" "$SPEC_DIR"' EXIT

RUNNER="$REPO_DIR/scripts/run_experiment.sh"
HOOK_MARKER="$TMP_DIR/hook-ran"
export HOOK_MARKER

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

cat > "$SPEC_DIR/untrusted.env" <<'SPEC'
EXPERIMENT_NAME="untrusted hook"
EXPERIMENT_BEFORE_SHELL='touch "$HOOK_MARKER"'
SPEC

untrusted_id="hook-trust-untrusted-$$"
if EXPERIMENT_RUN_ID="$untrusted_id" "$RUNNER" "$INTERNAL_RUN_ACTION" "$SPEC_DIR/untrusted.env" >"$TMP_DIR/untrusted.out" 2>"$TMP_DIR/untrusted.err"; then
  fail "untrusted host-shell hook was accepted"
fi
grep -q 'Host-shell hooks require EXPERIMENT_TRUSTED_SHELL=1: EXPERIMENT_BEFORE_SHELL' "$TMP_DIR/untrusted.err" || fail "missing fail-closed trust error"
[[ ! -e "$HOOK_MARKER" ]] || fail "untrusted host-shell hook executed"
[[ ! -e "$REPO_DIR/runs/$untrusted_id" ]] || fail "untrusted hook created a run directory"

cat > "$SPEC_DIR/trusted.env" <<'SPEC'
EXPERIMENT_NAME="trusted hook"
EXPERIMENT_TRUSTED_SHELL=1
EXPERIMENT_BEFORE_SHELL='touch "$HOOK_MARKER"'
SPEC

if EXPERIMENT_RUN_ID='../invalid' "$RUNNER" "$INTERNAL_RUN_ACTION" "$SPEC_DIR/trusted.env" >"$TMP_DIR/trusted.out" 2>"$TMP_DIR/trusted.err"; then
  fail "invalid run id was accepted"
fi
grep -q '^trusted_shell_hooks=EXPERIMENT_BEFORE_SHELL$' "$TMP_DIR/trusted.out" || fail "trusted hook allow-list was not logged"
grep -q 'Invalid EXPERIMENT_RUN_ID' "$TMP_DIR/trusted.err" || fail "trusted hook test did not stop at run-id validation"
[[ ! -e "$HOOK_MARKER" ]] || fail "trusted hook ran before run-id validation"

cat > "$SPEC_DIR/sql-only.env" <<'SPEC'
EXPERIMENT_NAME="SQL only"
EXPERIMENT_ASSERT_TRUE_SQL='SELECT true'
SPEC

if EXPERIMENT_RUN_ID='../invalid' "$RUNNER" "$INTERNAL_RUN_ACTION" "$SPEC_DIR/sql-only.env" >"$TMP_DIR/sql-only.out" 2>"$TMP_DIR/sql-only.err"; then
  fail "invalid run id was accepted for SQL-only spec"
fi
grep -q 'Invalid EXPERIMENT_RUN_ID' "$TMP_DIR/sql-only.err" || fail "SQL-only spec did not reach run-id validation"
if grep -q 'EXPERIMENT_TRUSTED_SHELL' "$TMP_DIR/sql-only.out" "$TMP_DIR/sql-only.err"; then
  fail "SQL-only spec was incorrectly gated by shell trust"
fi

cat > "$SPEC_DIR/invalid-marker.env" <<'SPEC'
EXPERIMENT_NAME="invalid marker"
EXPERIMENT_TRUSTED_SHELL=yes
SPEC

invalid_id="hook-trust-invalid-$$"
if EXPERIMENT_RUN_ID="$invalid_id" "$RUNNER" "$INTERNAL_RUN_ACTION" "$SPEC_DIR/invalid-marker.env" >"$TMP_DIR/invalid-marker.out" 2>"$TMP_DIR/invalid-marker.err"; then
  fail "invalid trust marker was accepted"
fi
grep -q 'EXPERIMENT_TRUSTED_SHELL must be 0 or 1: yes' "$TMP_DIR/invalid-marker.err" || fail "missing invalid-marker error"
[[ ! -e "$REPO_DIR/runs/$invalid_id" ]] || fail "invalid marker created a run directory"

cat > "$TMP_DIR/external.env" <<'SPEC'
EXPERIMENT_NAME="external"
SPEC

if "$RUNNER" show "$TMP_DIR/external.env" >"$TMP_DIR/external.out" 2>"$TMP_DIR/external.err"; then
  fail "external absolute experiment spec was accepted"
fi
grep -q 'outside scenario pack experiments' "$TMP_DIR/external.err" || fail "missing external-spec containment error"

ln -s "$TMP_DIR/external.env" "$SPEC_DIR/escape.env"
if "$RUNNER" show "$SPEC_DIR/escape.env" >"$TMP_DIR/escape.out" 2>"$TMP_DIR/escape.err"; then
  fail "experiment symlink escape was accepted"
fi
grep -q 'outside scenario pack experiments' "$TMP_DIR/escape.err" || fail "missing symlink-escape containment error"

spec_dir_id="${SPEC_DIR#"$REPO_DIR/experiments/"}"
if "$RUNNER" show "$spec_dir_id/../$spec_dir_id/trusted.env" >"$TMP_DIR/traversal.out" 2>"$TMP_DIR/traversal.err"; then
  fail "experiment parent traversal was accepted"
fi
grep -q 'parent traversal' "$TMP_DIR/traversal.err" || fail "missing parent-traversal error"

echo "PASS: experiment hook trust and spec containment"
