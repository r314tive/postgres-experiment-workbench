#!/usr/bin/env bash
set -euo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)"
TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/pgworkbench-process-lifecycle.XXXXXX")"
LIFECYCLE_STRESS_ITERATIONS="${PGWORKBENCH_LIFECYCLE_STRESS_ITERATIONS:-50}"
TEST_PIDS=()
TEST_STOP_FILES=()

# shellcheck source=../scripts/process_lifecycle.sh
source "$REPO_DIR/scripts/process_lifecycle.sh"

cleanup() {
  local index
  PGWORKBENCH_CLEANUP_GRACE_SECONDS=1
  pgworkbench_begin_owned_cleanup
  for index in "${!TEST_STOP_FILES[@]}"; do
    pgworkbench_request_owned_stop "${TEST_STOP_FILES[$index]}" >/dev/null 2>&1 || true
  done
  for index in "${!TEST_PIDS[@]}"; do
    pgworkbench_wait_after_stop_request \
      "${TEST_PIDS[$index]}" "${TEST_STOP_FILES[$index]}" >/dev/null 2>&1 || true
  done
  rm -rf -- "$TMP_DIR"
}
trap cleanup EXIT

track_process() {
  TEST_PIDS+=("$1")
  TEST_STOP_FILES+=("$2")
}

forget_processes() {
  TEST_PIDS=()
  TEST_STOP_FILES=()
}

if [[ ! "$LIFECYCLE_STRESS_ITERATIONS" =~ ^[1-9][0-9]*$ ]] ||
   (( LIFECYCLE_STRESS_ITERATIONS > 5000 )); then
  echo "FAIL: PGWORKBENCH_LIFECYCLE_STRESS_ITERATIONS must be in [1,5000]" >&2
  exit 1
fi

# An interactive/monitor-mode caller would assign each async supervisor its own
# process group before the supervisor can change options. Reject that state
# before forking rather than weakening the outer-runner containment invariant.
monitor_pid=""
set -m
monitor_status=0
pgworkbench_start_owned_process \
  monitor_pid "$TMP_DIR/monitor.stop" "$TMP_DIR/monitor.log" sleep 1 \
  2>"$TMP_DIR/monitor.err" || monitor_status="$?"
set +m
if [[ "$monitor_status" != "2" || -n "$monitor_pid" ]] ||
   ! grep -q 'job control to be disabled' "$TMP_DIR/monitor.err"; then
  echo "FAIL: lifecycle helper did not reject monitor-mode caller" >&2
  exit 1
fi

fifo_stop="$TMP_DIR/fifo.stop"
mkfifo "$fifo_stop"
fifo_started="$SECONDS"
fifo_status=0
pgworkbench_request_owned_stop "$fifo_stop" 2>"$TMP_DIR/fifo.err" || fifo_status="$?"
if [[ "$fifo_status" != "2" || $((SECONDS - fifo_started)) -gt 1 ]] ||
   ! grep -q 'unexpected file type' "$TMP_DIR/fifo.err"; then
  echo "FAIL: stop request did not promptly reject an existing FIFO" >&2
  exit 1
fi

supervisor_pid=""
collision_stop="$TMP_DIR/output-name-collision.stop"
collision_ready="$TMP_DIR/output-name-collision.ready"
pgworkbench_start_owned_process \
  supervisor_pid "$collision_stop" "$TMP_DIR/output-name-collision.log" \
  bash -c 'trap "exit 0" TERM; : > "$1"; while true; do sleep 0.05; done' \
  pgworkbench-output-name-collision "$collision_ready"
if [[ ! "$supervisor_pid" =~ ^[1-9][0-9]*$ ]]; then
  echo "FAIL: natural supervisor_pid output name was dynamically shadowed" >&2
  exit 1
fi
track_process "$supervisor_pid" "$collision_stop"
collision_deadline=$((SECONDS + 2))
while [[ ! -e "$collision_ready" ]]; do
  if ! pgworkbench_owned_process_running "$supervisor_pid" || (( SECONDS >= collision_deadline )); then
    echo "FAIL: output-name collision target did not publish readiness" >&2
    exit 1
  fi
  sleep 0.01
done
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$collision_stop"
pgworkbench_wait_after_stop_request "$supervisor_pid" "$collision_stop" >/dev/null
forget_processes

readonly READONLY_PID=""
jobs -pr > "$TMP_DIR/jobs-before-readonly-output"
readonly_output_status=0
pgworkbench_start_owned_process \
  READONLY_PID "$TMP_DIR/readonly-output.stop" "$TMP_DIR/readonly-output.log" sleep 1 \
  2>"$TMP_DIR/readonly-output.err" || readonly_output_status="$?"
jobs -pr > "$TMP_DIR/jobs-after-readonly-output"
if [[ "$readonly_output_status" != "2" ]] ||
   ! grep -q 'writable empty scalar' "$TMP_DIR/readonly-output.err" ||
   ! cmp -s "$TMP_DIR/jobs-before-readonly-output" "$TMP_DIR/jobs-after-readonly-output"; then
  echo "FAIL: readonly output variable was not rejected before process launch" >&2
  exit 1
fi

# Environment/spec restoration may export an otherwise valid empty lifecycle
# variable. The helper must accept that shape, strip export before forking, and
# keep the supervisor PID private to the runner shell.
exported_output_pid=""
export exported_output_pid
exported_output_stop="$TMP_DIR/exported-output.stop"
exported_output_ready="$TMP_DIR/exported-output.ready"
pgworkbench_start_owned_process \
  exported_output_pid "$exported_output_stop" "$TMP_DIR/exported-output.log" \
  bash -c '
    [[ "${exported_output_pid+x}" != x ]] || exit 91
    trap "exit 0" TERM
    : > "$1"
    while true; do sleep 0.05; done
  ' pgworkbench-exported-output "$exported_output_ready"
track_process "$exported_output_pid" "$exported_output_stop"
exported_output_deadline=$((SECONDS + 2))
while [[ ! -e "$exported_output_ready" ]]; do
  if ! pgworkbench_owned_process_running "$exported_output_pid" ||
     (( SECONDS >= exported_output_deadline )); then
    echo "FAIL: exported output variable leaked into the owned process" >&2
    exit 1
  fi
  sleep 0.01
done
if builtin export -p | grep -q "declare -x exported_output_pid="; then
  echo "FAIL: owned-process output variable remained exported" >&2
  exit 1
fi
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$exported_output_stop"
pgworkbench_wait_after_stop_request "$exported_output_pid" "$exported_output_stop" >/dev/null
forget_processes

# A raw SIGTERM that happened before cleanup is a real child failure. Creating
# the stop token afterwards must not retroactively classify it as expected.
self_term_pid=""
self_term_stop="$TMP_DIR/self-term.stop"
self_term_ready="$TMP_DIR/self-term.ready"
pgworkbench_start_owned_process \
  self_term_pid "$self_term_stop" "$TMP_DIR/self-term.log" \
  bash -c ': > "$1"; kill -TERM "$$"' pgworkbench-self-term "$self_term_ready"
track_process "$self_term_pid" "$self_term_stop"
self_term_deadline=$((SECONDS + 2))
while pgworkbench_owned_process_running "$self_term_pid"; do
  if (( SECONDS >= self_term_deadline )); then
    echo "FAIL: self-TERM fixture did not finish" >&2
    exit 1
  fi
  sleep 0.01
done
pgworkbench_request_owned_stop "$self_term_stop"
self_term_status=0
pgworkbench_wait_after_stop_request "$self_term_pid" "$self_term_stop" || self_term_status="$?"
forget_processes
if [[ "$self_term_status" != "143" || ! -e "$self_term_ready" ]]; then
  echo "FAIL: pre-cleanup self-SIGTERM was normalized (status=$self_term_status)" >&2
  exit 1
fi

# Conversely, a direct child terminated by the supervisor after its observed
# stop token is an expected cooperative lifecycle transition.
owned_term_pid=""
owned_term_stop="$TMP_DIR/owned-term.stop"
owned_term_ready="$TMP_DIR/owned-term.ready"
pgworkbench_start_owned_process \
  owned_term_pid "$owned_term_stop" "$TMP_DIR/owned-term.log" \
  bash -c ': > "$1"; exec sleep 60' pgworkbench-owned-term "$owned_term_ready"
track_process "$owned_term_pid" "$owned_term_stop"
owned_term_deadline=$((SECONDS + 2))
while [[ ! -e "$owned_term_ready" ]]; do
  if ! pgworkbench_owned_process_running "$owned_term_pid" || (( SECONDS >= owned_term_deadline )); then
    echo "FAIL: supervisor-TERM fixture did not publish readiness" >&2
    exit 1
  fi
  sleep 0.01
done
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$owned_term_stop"
owned_term_status=0
pgworkbench_wait_after_stop_request "$owned_term_pid" "$owned_term_stop" || owned_term_status="$?"
forget_processes
if [[ "$owned_term_status" != "0" ]]; then
  echo "FAIL: supervisor-delivered SIGTERM was not classified as expected (status=$owned_term_status)" >&2
  exit 1
fi

delegate_bin="$TMP_DIR/pgworkbench-delegate"
cat > "$delegate_bin" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$@" > "${DELEGATE_CAPTURE:?}"
SCRIPT
chmod +x "$delegate_bin"
expected_delegate=$'experiment\nrun\n--runtime\nnative\n--run-id\ndelegated-run\n--timeout\n2m\n--cleanup-grace\n3s\nsmoke'
for supervised_value in 0 1; do
  delegate_capture="$TMP_DIR/delegate-$supervised_value.args"
  PGWORKBENCH_SUPERVISED="$supervised_value" \
  PGWORKBENCH_BIN="$delegate_bin" \
  PGWORKBENCH_RUNTIME=native \
  PGWORKBENCH_EXECUTION_TIMEOUT=2m \
  PGWORKBENCH_CLEANUP_GRACE=3s \
  EXPERIMENT_RUN_ID=delegated-run \
  DELEGATE_CAPTURE="$delegate_capture" \
    "$REPO_DIR/scripts/run_experiment.sh" run smoke
  if [[ "$(<"$delegate_capture")" != "$expected_delegate" ]]; then
    echo "FAIL: direct experiment shell entry did not delegate through the Go CLI (PGWORKBENCH_SUPERVISED=$supervised_value)" >&2
    exit 1
  fi
done

shorthand_capture="$TMP_DIR/delegate-shorthand.args"
PGWORKBENCH_SUPERVISED=1 \
PGWORKBENCH_BIN="$delegate_bin" \
PGWORKBENCH_RUNTIME=native \
PGWORKBENCH_EXECUTION_TIMEOUT=2m \
PGWORKBENCH_CLEANUP_GRACE=3s \
EXPERIMENT_RUN_ID=delegated-run \
DELEGATE_CAPTURE="$shorthand_capture" \
  "$REPO_DIR/scripts/run_experiment.sh" smoke
if [[ "$(<"$shorthand_capture")" != "$expected_delegate" ]]; then
  echo "FAIL: shorthand experiment shell entry trusted a forged supervision marker" >&2
  exit 1
fi

internal_capture="$TMP_DIR/internal-without-supervision.args"
internal_status=0
PGWORKBENCH_SUPERVISED=0 \
PGWORKBENCH_BIN="$delegate_bin" \
DELEGATE_CAPTURE="$internal_capture" \
  "$REPO_DIR/scripts/run_experiment.sh" __pgworkbench_internal_run_v1 smoke \
  >"$TMP_DIR/internal-without-supervision.out" 2>"$TMP_DIR/internal-without-supervision.err" || internal_status="$?"
if [[ "$internal_status" != "2" || -e "$internal_capture" ]] ||
   ! grep -q 'Refusing internal experiment route without Go supervision' "$TMP_DIR/internal-without-supervision.err"; then
  echo "FAIL: private experiment route did not fail closed without Go supervision" >&2
  exit 1
fi

# The private shell body receives the logical scenario-pack path for identity,
# but it must source the immutable bytes selected by the Go runner. This models
# a valid A -> B replacement of the live path after Go has built its plan.
execution_spec="$TMP_DIR/selected-experiment.env"
printf '%s\n' 'EXPERIMENT_NAME=selected-A' 'EXPERIMENT_STATE_WRITER=snapshot-selected-A' > "$execution_spec"
if command -v shasum >/dev/null 2>&1; then
  execution_spec_digest="$(shasum -a 256 -- "$execution_spec" | awk '{print $1}')"
else
  execution_spec_digest="$(sha256sum -- "$execution_spec" | awk '{print $1}')"
fi
snapshot_status=0
(
  unset EXPERIMENT_STATE_WRITER
  PGWORKBENCH_SUPERVISED=1 \
  PGWORKBENCH_EXECUTION_SPEC_FILE="$execution_spec" \
  EXPERIMENT_SPEC_SHA256="$execution_spec_digest" \
  EXPERIMENT_RUN_ID=snapshot-binding-shell \
    "$REPO_DIR/scripts/run_experiment.sh" __pgworkbench_internal_run_v1 \
      "$REPO_DIR/experiments/smoke.env" \
      >"$TMP_DIR/snapshot-binding.out" 2>"$TMP_DIR/snapshot-binding.err"
) || snapshot_status="$?"
if [[ "$snapshot_status" != "2" ]] ||
   ! grep -q 'Unsupported EXPERIMENT_STATE_WRITER: snapshot-selected-A' "$TMP_DIR/snapshot-binding.err"; then
  echo "FAIL: private experiment route did not source the runner-selected spec bytes" >&2
  exit 1
fi

PGWORKBENCH_CLEANUP_GRACE_SECONDS=1
export PGWORKBENCH_CLEANUP_GRACE_SECONDS

metrics="$TMP_DIR/metrics.csv"
ready="$TMP_DIR/metrics.ready"
stop="$TMP_DIR/metrics.stop"
returned="$TMP_DIR/readiness-returned"
premature="$TMP_DIR/readiness-returned-before-marker"
collector_pid=""
pgworkbench_start_owned_process collector_pid "$stop" "$TMP_DIR/collector.log" \
  bash -c '
    trap "exit 0" TERM
    printf "header\n" > "$1"
    sleep 0.25
    if [[ -e "$3" ]]; then : > "$4"; fi
    printf "validated-row\n" >> "$1"
    (umask 077; mkdir -- "$2")
    while true; do sleep 0.05; done
  ' pgworkbench-test-collector "$metrics" "$ready" "$returned" "$premature"
track_process "$collector_pid" "$stop"
pgworkbench_wait_for_metrics_ready "$collector_pid" "$metrics" "$ready" "$stop" 2
: > "$returned"
if [[ -e "$premature" || -e "$ready" ]]; then
  echo "FAIL: readiness returned before publication or retained its marker" >&2
  exit 1
fi
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$stop"
collector_status=0
pgworkbench_wait_after_stop_request "$collector_pid" "$stop" || collector_status="$?"
forget_processes
if [[ "$collector_status" != "0" ]]; then
  echo "FAIL: cooperative metrics collector exited $collector_status" >&2
  exit 1
fi

printf 'evidence\n' > "$TMP_DIR/evidence"
printf 'wrong\n' > "$TMP_DIR/wrong.ready"
wrong_status=0
pgworkbench_metrics_evidence_ready "$TMP_DIR/evidence" "$TMP_DIR/wrong.ready" || wrong_status="$?"
if [[ "$wrong_status" != "2" ]]; then
  echo "FAIL: readiness accepted or misclassified a regular-file token" >&2
  exit 1
fi
mkdir -m 700 "$TMP_DIR/nonempty.ready"
: > "$TMP_DIR/nonempty.ready/member"
nonempty_pid=""
nonempty_stop="$TMP_DIR/nonempty.stop"
pgworkbench_start_owned_process \
  nonempty_pid "$nonempty_stop" "$TMP_DIR/nonempty.log" sleep 60
track_process "$nonempty_pid" "$nonempty_stop"
nonempty_status=0
pgworkbench_wait_for_metrics_ready \
  "$nonempty_pid" "$TMP_DIR/evidence" "$TMP_DIR/nonempty.ready" "$nonempty_stop" 1 \
  2>"$TMP_DIR/nonempty.err" || nonempty_status="$?"
if ! pgworkbench_owned_process_running "$nonempty_pid"; then
  forget_processes
fi
if [[ "$nonempty_status" != "2" ]] || ! grep -q 'not an empty owned directory' "$TMP_DIR/nonempty.err"; then
  echo "FAIL: readiness accepted a nonempty directory token" >&2
  exit 1
fi
mkfifo "$TMP_DIR/fifo.ready"
fifo_ready_started="$SECONDS"
fifo_ready_status=0
pgworkbench_metrics_evidence_ready "$TMP_DIR/evidence" "$TMP_DIR/fifo.ready" || fifo_ready_status="$?"
if [[ "$fifo_ready_status" != "2" || $((SECONDS - fifo_ready_started)) -gt 1 ]]; then
  echo "FAIL: readiness did not promptly reject a FIFO token" >&2
  exit 1
fi
ln -s "$TMP_DIR/evidence" "$TMP_DIR/evidence-link"
mkdir -m 700 "$TMP_DIR/link.ready"
symlink_status=0
pgworkbench_metrics_evidence_ready "$TMP_DIR/evidence-link" "$TMP_DIR/link.ready" || symlink_status="$?"
if [[ "$symlink_status" != "2" ]]; then
  echo "FAIL: readiness accepted or misclassified an evidence symlink" >&2
  exit 1
fi
mkdir -m 700 "$TMP_DIR/ready-target"
ln -s "$TMP_DIR/ready-target" "$TMP_DIR/ready-link"
ready_symlink_status=0
pgworkbench_metrics_evidence_ready "$TMP_DIR/evidence" "$TMP_DIR/ready-link" || ready_symlink_status="$?"
if [[ "$ready_symlink_status" != "2" ]]; then
  echo "FAIL: readiness accepted or misclassified a ready-token symlink" >&2
  exit 1
fi

# Fast fixed-sample collectors may publish readiness and exit between adjacent
# probes. The final token check must win that race without discarding status.
# Publication deliberately uses plain mkdir under umask 077: on BSD, `mkdir -m`
# can expose the directory before a trailing path-based chmod, allowing the
# consumer's rmdir to make an otherwise valid producer exit nonzero.
for ((iteration = 1; iteration <= LIFECYCLE_STRESS_ITERATIONS; iteration++)); do
  fast_metrics="$TMP_DIR/fast-metrics-$iteration.csv"
  fast_ready="$TMP_DIR/fast-ready-$iteration"
  fast_stop="$TMP_DIR/fast-stop-$iteration"
  fast_pid=""
  pgworkbench_start_owned_process fast_pid "$fast_stop" "$TMP_DIR/fast-$iteration.log" \
    bash -c 'printf "header\\nrow\\n" > "$1"; (umask 077; mkdir -- "$2")' \
    pgworkbench-fast-collector "$fast_metrics" "$fast_ready"
  track_process "$fast_pid" "$fast_stop"
  fast_ready_status=0
  pgworkbench_wait_for_metrics_ready \
    "$fast_pid" "$fast_metrics" "$fast_ready" "$fast_stop" 2 || fast_ready_status="$?"
  fast_exit_status=0
  pgworkbench_wait_owned_process "$fast_pid" "$fast_stop" || fast_exit_status="$?"
  forget_processes
  if [[ "$fast_ready_status" != "0" || "$fast_exit_status" != "0" || -e "$fast_ready" ]]; then
    echo "FAIL: fast readiness publication race failed at iteration $iteration (ready=$fast_ready_status exit=$fast_exit_status)" >&2
    exit 1
  fi
done

early_pid=""
early_stop="$TMP_DIR/early.stop"
pgworkbench_start_owned_process early_pid "$early_stop" "$TMP_DIR/early.log" bash -c 'exit 23'
track_process "$early_pid" "$early_stop"
early_status=0
pgworkbench_wait_for_metrics_ready \
  "$early_pid" "$TMP_DIR/early.csv" "$TMP_DIR/early.ready" "$early_stop" 2 \
  2>"$TMP_DIR/early.err" || early_status="$?"
forget_processes
if [[ "$early_status" != "23" ]] ||
   ! grep -q 'before publishing its first complete sample' "$TMP_DIR/early.err"; then
  echo "FAIL: pre-readiness collector exit was not preserved" >&2
  exit 1
fi

timeout_pid=""
timeout_stop="$TMP_DIR/timeout.stop"
pgworkbench_start_owned_process timeout_pid "$timeout_stop" "$TMP_DIR/timeout.log" sleep 60
track_process "$timeout_pid" "$timeout_stop"
timeout_status=0
pgworkbench_wait_for_metrics_ready \
  "$timeout_pid" "$TMP_DIR/timeout.csv" "$TMP_DIR/timeout.ready" "$timeout_stop" 1 \
  2>"$TMP_DIR/timeout.err" || timeout_status="$?"
if ! pgworkbench_owned_process_running "$timeout_pid"; then
  forget_processes
fi
if [[ "$timeout_status" != "124" ]] ||
   ! grep -q 'did not publish its first complete sample' "$TMP_DIR/timeout.err"; then
  echo "FAIL: metrics readiness timeout was not bounded" >&2
  exit 1
fi

# Starting near the end of a Bash SECONDS tick must not collapse a one-second
# readiness budget to the remaining fraction of that tick.
boundary_seconds="$SECONDS"
while (( SECONDS == boundary_seconds )); do :; done
sleep 0.8
boundary_pid=""
boundary_stop="$TMP_DIR/boundary.stop"
pgworkbench_start_owned_process boundary_pid "$boundary_stop" "$TMP_DIR/boundary.log" \
  bash -c '
    trap "exit 0" TERM
    sleep 0.35
    printf "evidence\n" > "$1"
    (umask 077; mkdir -- "$2")
    while true; do sleep 0.05; done
  ' pgworkbench-boundary "$TMP_DIR/boundary.csv" "$TMP_DIR/boundary.ready"
track_process "$boundary_pid" "$boundary_stop"
pgworkbench_wait_for_metrics_ready \
  "$boundary_pid" "$TMP_DIR/boundary.csv" "$TMP_DIR/boundary.ready" "$boundary_stop" 1
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$boundary_stop"
pgworkbench_wait_after_stop_request "$boundary_pid" "$boundary_stop" >/dev/null
forget_processes

for ((iteration = 1; iteration <= LIFECYCLE_STRESS_ITERATIONS; iteration++)); do
  target_ready="$TMP_DIR/target-ready-$iteration"
  target_stop="$TMP_DIR/target-stop-$iteration"
  target_pid=""
  pgworkbench_start_owned_process target_pid "$target_stop" "$TMP_DIR/target-$iteration.log" \
    bash -c 'trap "exit 0" TERM; printf ready > "$1"; while true; do sleep 0.05; done' \
    pgworkbench-test-target "$target_ready"
  track_process "$target_pid" "$target_stop"
  marker_deadline=$((SECONDS + 2))
  while [[ ! -s "$target_ready" ]]; do
    if ! pgworkbench_owned_process_running "$target_pid" || (( SECONDS >= marker_deadline )); then
      echo "FAIL: stress target did not publish readiness at iteration $iteration" >&2
      exit 1
    fi
    sleep 0.01
  done
  pgworkbench_begin_owned_cleanup
  pgworkbench_request_owned_stop "$target_stop"
  target_status=0
  pgworkbench_wait_after_stop_request "$target_pid" "$target_stop" || target_status="$?"
  forget_processes
  if [[ "$target_status" != "0" ]] || pgworkbench_owned_process_running "$target_pid"; then
    echo "FAIL: cooperative owned process was not reaped at iteration $iteration (status=$target_status)" >&2
    exit 1
  fi
done

# A TERM-ignoring direct child is escalated through Bash's live job handle;
# callers never signal a cached PID after the child has become waitable.
forced_pid=""
forced_stop="$TMP_DIR/forced.stop"
pgworkbench_start_owned_process forced_pid "$forced_stop" "$TMP_DIR/forced.log" \
  bash -c 'trap "" TERM; printf ready > "$1"; while true; do :; done' \
  pgworkbench-test-forced "$TMP_DIR/forced.ready"
track_process "$forced_pid" "$forced_stop"
forced_deadline=$((SECONDS + 2))
while [[ ! -s "$TMP_DIR/forced.ready" ]]; do
  if ! pgworkbench_owned_process_running "$forced_pid" || (( SECONDS >= forced_deadline )); then
    echo "FAIL: forced-kill target did not enter its TERM-ignoring loop" >&2
    exit 1
  fi
  sleep 0.01
done
pgworkbench_begin_owned_cleanup
pgworkbench_request_owned_stop "$forced_stop"
forced_status=0
pgworkbench_wait_after_stop_request "$forced_pid" "$forced_stop" || forced_status="$?"
if ! pgworkbench_owned_process_running "$forced_pid"; then
  forget_processes
fi
if [[ "$forced_status" != "137" ]]; then
  echo "FAIL: SIGKILL escalation did not preserve child status 137 (status=$forced_status)" >&2
  exit 1
fi

# No helper code may isolate an owned command into a nested process group. The
# Go experimentrun tests exercise the corresponding residual-group backstop.
if grep -Eq 'Setpgid|setsid|kill[[:space:]]+-[^[:space:]]+[[:space:]]+-\$' \
  "$REPO_DIR/scripts/process_lifecycle.sh"; then
  echo "FAIL: lifecycle helper contains nested process-group isolation or signalling" >&2
  exit 1
fi

# A direct leader can deliberately leave a descendant. The helper must not
# mis-signal by cached PID; experimentrun owns detection and final group cleanup.
residual_pid=""
residual_stop="$TMP_DIR/residual.stop"
pgworkbench_start_owned_process residual_pid "$residual_stop" "$TMP_DIR/residual.log" \
  bash -c 'sleep 60 & printf "%s\n" "$!" > "$1"; exit 0' \
  pgworkbench-test-residual "$TMP_DIR/residual-child.pid"
track_process "$residual_pid" "$residual_stop"
residual_status=0
pgworkbench_wait_owned_process "$residual_pid" "$residual_stop" || residual_status="$?"
forget_processes
if [[ "$residual_status" != "0" ]]; then
  echo "FAIL: residual fixture leader status changed: $residual_status" >&2
  exit 1
fi
residual_child="$(<"$TMP_DIR/residual-child.pid")"
if ! kill -0 "$residual_child" >/dev/null 2>&1; then
  echo "FAIL: residual fixture did not exercise outer containment boundary" >&2
  exit 1
fi
kill -TERM "$residual_child" >/dev/null 2>&1 || true
residual_deadline=$((SECONDS + 2))
while kill -0 "$residual_child" >/dev/null 2>&1 && (( SECONDS < residual_deadline )); do
  sleep 0.02
done
if kill -0 "$residual_child" >/dev/null 2>&1; then
  kill -KILL "$residual_child" >/dev/null 2>&1 || true
fi

echo "PASS: readiness-token and same-group owned process lifecycle"
