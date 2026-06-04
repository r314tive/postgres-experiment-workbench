SHELL := /usr/bin/env bash

COMPOSE ?= docker compose
ENV_FILE ?= $(if $(wildcard .env),.env,.env.example)
PROFILE ?= smoke
PROFILE_SIZE ?= small
PROFILE_SECONDS ?= 30
PG_CONFIG ?= default
TOPOLOGY ?= single
WORKLOAD_SQL ?= 10_run.sql
WORKLOAD ?= wait-xacts
WORKLOAD_SPEC ?= workloads/sql/smoke-run.env
EXPERIMENT_SPEC ?= smoke
EXPERIMENT_REPEAT_COUNT ?= 3
EXPERIMENT_REPEAT_ID ?=
MATRIX_SPEC ?= smoke
PATCHSET ?= chaos/master
SOURCE_WORKLOAD_SPEC ?= pg-source/check
SOURCE_CHECK_PATH ?= generated/pg-source
DATASET_SPEC ?= synthetic/items
DATASET_SIZE ?= small
DATASET_SEED ?= 1
DATASET_SCHEMA ?= dataset_synthetic
BASELINE_RUN ?=
CANDIDATE_RUN ?=
RUN_DIR ?=
QUICKSTART_RUN_ID ?= quickstart-$(shell date -u +%Y%m%d_%H%M%S)
SPEC_KIND ?= workload
SPEC_ID ?=
SUMMARY_INPUT ?=
SUMMARY_OUT ?=
HISTORY_INPUTS ?=
HISTORY_OUT ?=
SCAN_PATHS ?= logs generated
DOCTOR_FLAGS ?=
METRICS_INTERVAL ?= 1
METRICS_DURATION ?= 30
METRICS_SAMPLES ?=
METRICS_OUT ?=
NOISIA_DURATION ?= 60
NOISIA_JOBS ?= 2
GO ?= go
GO_CACHE ?= $(CURDIR)/.tmp/go-cache
GO_MOD_CACHE ?= $(CURDIR)/.tmp/go-mod-cache
VERSION ?= 0.0.0-dev
BUILD_COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
RELEASE_DIR ?= generated/release
RELEASE_PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64
RELEASE_CHECKSUM_FILE ?= $(RELEASE_DIR)/pgworkbench-$(VERSION)-SHA256SUMS.txt
PGWORKBENCH_LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.builtAt=$(BUILD_DATE)

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf '%s\n' 'Targets:'
	@printf '  %-24s %s\n' 'make docker-up' 'Start PostgreSQL workbench'
	@printf '  %-24s %s\n' 'make docker-down' 'Stop PostgreSQL workbench, keep volume'
	@printf '  %-24s %s\n' 'make docker-reset' 'Recreate PostgreSQL volume'
	@printf '  %-24s %s\n' 'make quickstart-plan' 'Preview the smoke experiment quickstart'
	@printf '  %-24s %s\n' 'make quickstart' 'Run smoke quickstart and write report.md'
	@printf '  %-24s %s\n' 'make doctor' 'Check local workbench prerequisites'
	@printf '  %-24s %s\n' 'make topology-list' 'List topology specs'
	@printf '  %-24s %s\n' 'make topology-show' 'Show TOPOLOGY'
	@printf '  %-24s %s\n' 'make topology-inspect' 'Inspect TOPOLOGY runtime shape with Go CLI'
	@printf '  %-24s %s\n' 'make topology-ps' 'Parse live TOPOLOGY Compose state with Go CLI'
	@printf '  %-24s %s\n' 'make topology-up' 'Start TOPOLOGY'
	@printf '  %-24s %s\n' 'make topology-reset' 'Recreate TOPOLOGY volumes'
	@printf '  %-24s %s\n' 'make topology-status' 'Show TOPOLOGY runtime status'
	@printf '  %-24s %s\n' 'make topology-down' 'Stop TOPOLOGY'
	@printf '  %-24s %s\n' 'make psql' 'Open psql'
	@printf '  %-24s %s\n' 'make pg-config-apply' 'Apply PG_CONFIG to disposable PostgreSQL'
	@printf '  %-24s %s\n' 'make snapshot' 'Capture PostgreSQL snapshot artifacts'
	@printf '  %-24s %s\n' 'make profile-list' 'List profiles'
	@printf '  %-24s %s\n' 'make profile-show' 'Show PROFILE metadata'
	@printf '  %-24s %s\n' 'make profile-validate' 'Validate profile metadata and required files'
	@printf '  %-24s %s\n' 'make patchset-list' 'List PostgreSQL source patchsets'
	@printf '  %-24s %s\n' 'make patchset-show' 'Show PATCHSET metadata'
	@printf '  %-24s %s\n' 'make patchset-validate' 'Validate patchset metadata and files'
	@printf '  %-24s %s\n' 'make source-plan' 'Preview PostgreSQL source-check plan'
	@printf '  %-24s %s\n' 'make source-classify' 'Classify PostgreSQL source-check artifacts'
	@printf '  %-24s %s\n' 'make profile-setup' 'Run profiles/$(PROFILE)/sql/00_setup.sql'
	@printf '  %-24s %s\n' 'make profile-run' 'Run profiles/$(PROFILE)/sql/$(WORKLOAD_SQL)'
	@printf '  %-24s %s\n' 'make profile-reset' 'Run setup and run SQL for PROFILE'
	@printf '  %-24s %s\n' 'make monitor' 'Show basic PostgreSQL activity/statistics'
	@printf '  %-24s %s\n' 'make metrics-sample' 'Sample generic PostgreSQL metrics to CSV'
	@printf '  %-24s %s\n' 'make scan-artifacts' 'Scan logs/artifacts for PG failure evidence'
	@printf '  %-24s %s\n' 'make scan-artifacts-go' 'Scan logs/artifacts with Go CLI'
	@printf '  %-24s %s\n' 'make privacy-scan' 'Scan public files for sensitive-looking text'
	@printf '  %-24s %s\n' 'make dataset-list' 'List dataset specs'
	@printf '  %-24s %s\n' 'make dataset-show' 'Show DATASET_SPEC'
	@printf '  %-24s %s\n' 'make dataset-load' 'Load DATASET_SPEC'
	@printf '  %-24s %s\n' 'make experiment-list' 'List experiment specs'
	@printf '  %-24s %s\n' 'make experiment-show' 'Show EXPERIMENT_SPEC'
	@printf '  %-24s %s\n' 'make experiment-plan' 'Render EXPERIMENT_SPEC execution plan'
	@printf '  %-24s %s\n' 'make experiment-run' 'Run EXPERIMENT_SPEC into runs/<run-id>'
	@printf '  %-24s %s\n' 'make experiment-verify' 'Verify RUN_DIR artifact integrity'
	@printf '  %-24s %s\n' 'make experiment-report' 'Render Markdown report for RUN_DIR'
	@printf '  %-24s %s\n' 'make experiment-report-go' 'Render Markdown report with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-summary' 'Summarize a repeat/matrix/run series'
	@printf '  %-24s %s\n' 'make experiment-summary-go' 'Summarize runs with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-history' 'Compare repeat/matrix/run series history'
	@printf '  %-24s %s\n' 'make experiment-history-go' 'Compare run history with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-repeat' 'Run EXPERIMENT_SPEC repeatedly'
	@printf '  %-24s %s\n' 'make experiment-compare' 'Compare BASELINE_RUN and CANDIDATE_RUN'
	@printf '  %-24s %s\n' 'make experiment-compare-go' 'Compare runs with Go CLI'
	@printf '  %-24s %s\n' 'make matrix-list' 'List experiment matrix specs'
	@printf '  %-24s %s\n' 'make matrix-show' 'Show MATRIX_SPEC'
	@printf '  %-24s %s\n' 'make matrix-plan' 'Preview MATRIX_SPEC combinations'
	@printf '  %-24s %s\n' 'make matrix-run' 'Run MATRIX_SPEC combinations'
	@printf '  %-24s %s\n' 'make spec-list' 'List SPEC_KIND specs with Go CLI'
	@printf '  %-24s %s\n' 'make spec-show' 'Show SPEC_KIND/SPEC_ID with Go CLI'
	@printf '  %-24s %s\n' 'make spec-reference' 'Render env spec reference with Go CLI'
	@printf '  %-24s %s\n' 'make spec-schema' 'Render env spec JSON Schema with Go CLI'
	@printf '  %-24s %s\n' 'make spec-docs-check' 'Check tracked env spec docs/schema are current'
	@printf '  %-24s %s\n' 'make spec-validate' 'Validate env specs with Go CLI'
	@printf '  %-24s %s\n' 'make workload-list' 'List workload specs'
	@printf '  %-24s %s\n' 'make workload-show' 'Show WORKLOAD_SPEC'
	@printf '  %-24s %s\n' 'make workload-run' 'Run WORKLOAD_SPEC'
	@printf '  %-24s %s\n' 'make workload-start' 'Run profile SQL in the background'
	@printf '  %-24s %s\n' 'make workload-start-spec' 'Run WORKLOAD_SPEC in the background'
	@printf '  %-24s %s\n' 'make workload-start-sql' 'Run SQL=path in the background'
	@printf '  %-24s %s\n' 'make workload-start-noisia' 'Run noisia workload in the background'
	@printf '  %-24s %s\n' 'make workload-status' 'Show background workload status'
	@printf '  %-24s %s\n' 'make workload-log' 'Tail background workload log'
	@printf '  %-24s %s\n' 'make workload-stop' 'Stop background workload'
	@printf '  %-24s %s\n' 'make run-sql SQL=path' 'Run a SQL file with logs'
	@printf '  %-24s %s\n' 'make noisia-help' 'Show noisia help'
	@printf '  %-24s %s\n' 'make noisia-wait' 'Run noisia wait transactions'
	@printf '  %-24s %s\n' 'make noisia-temp' 'Run noisia temp files'
	@printf '  %-24s %s\n' 'make go-test' 'Run Go unit tests'
	@printf '  %-24s %s\n' 'make pgworkbench' 'Build Go CLI into generated/bin'
	@printf '  %-24s %s\n' 'make release-snapshot' 'Build pgworkbench release archives'
	@printf '  %-24s %s\n' 'make check' 'Run static and no-Docker checks'
	@printf '  %-24s %s\n' 'make test' 'Run profile and workload verification'
	@printf '  %-24s %s\n' 'make release-check' 'Run release readiness checks'

.PHONY: docker-up
docker-up:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh up "$(TOPOLOGY)"

.PHONY: docker-down
docker-down:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh down "$(TOPOLOGY)"

.PHONY: docker-reset
docker-reset:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh reset "$(TOPOLOGY)"
	@if [[ "$(PG_CONFIG)" != "default" ]]; then ./scripts/apply_pg_config.sh "$(PG_CONFIG)"; fi

.PHONY: quickstart-plan
quickstart-plan:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan smoke

.PHONY: quickstart
quickstart:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" EXPERIMENT_RUN_ID="$(QUICKSTART_RUN_ID)" ./scripts/run_experiment.sh run smoke
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify "runs/$(QUICKSTART_RUN_ID)"
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report run "runs/$(QUICKSTART_RUN_ID)" > "runs/$(QUICKSTART_RUN_ID)/report.md"
	@printf 'Quickstart run: %s\n' "runs/$(QUICKSTART_RUN_ID)"
	@printf 'Quickstart report: %s\n' "runs/$(QUICKSTART_RUN_ID)/report.md"

.PHONY: doctor
doctor:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench doctor $(DOCTOR_FLAGS)

.PHONY: topology-list
topology-list:
	./scripts/topology.sh list

.PHONY: topology-show
topology-show:
	./scripts/topology.sh show "$(TOPOLOGY)"

.PHONY: topology-inspect
topology-inspect:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology inspect "$(TOPOLOGY)"

.PHONY: topology-ps
topology-ps:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology ps "$(TOPOLOGY)"

.PHONY: topology-up
topology-up:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh up "$(TOPOLOGY)"

.PHONY: topology-reset
topology-reset:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh reset "$(TOPOLOGY)"

.PHONY: topology-status
topology-status:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh status "$(TOPOLOGY)"

.PHONY: topology-down
topology-down:
	COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/topology.sh down "$(TOPOLOGY)"

.PHONY: psql
psql: docker-up
	./scripts/psql.sh

.PHONY: pg-config-apply
pg-config-apply: docker-up
	./scripts/apply_pg_config.sh "$(PG_CONFIG)"

.PHONY: snapshot
snapshot: docker-up
	./scripts/snapshot_pg.sh

.PHONY: profile-list
profile-list:
	./scripts/profile_catalog.sh list

.PHONY: profile-show
profile-show:
	./scripts/profile_catalog.sh show "$(PROFILE)"

.PHONY: profile-validate
profile-validate:
	./scripts/profile_catalog.sh validate

.PHONY: patchset-list
patchset-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench patchset list

.PHONY: patchset-show
patchset-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench patchset show "$(PATCHSET)"

.PHONY: patchset-validate
patchset-validate:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench patchset validate

.PHONY: source-plan
source-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench source plan "$(SOURCE_WORKLOAD_SPEC)"

.PHONY: source-classify
source-classify:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench source classify "$(SOURCE_CHECK_PATH)"

.PHONY: profile-setup
profile-setup: docker-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_profile_sql.sh "$(PROFILE)" 00_setup.sql

.PHONY: profile-run
profile-run: docker-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_profile_sql.sh "$(PROFILE)" "$(WORKLOAD_SQL)"

.PHONY: profile-reset
profile-reset: profile-setup profile-run

.PHONY: monitor
monitor: docker-up
	./scripts/psql.sh -f sql/monitor.sql

.PHONY: metrics-sample
metrics-sample: docker-up
	METRICS_INTERVAL="$(METRICS_INTERVAL)" METRICS_DURATION="$(METRICS_DURATION)" METRICS_SAMPLES="$(METRICS_SAMPLES)" METRICS_OUT="$(METRICS_OUT)" ./scripts/sample_metrics.sh

.PHONY: workload-list
workload-list:
	./scripts/run_workload.sh list

.PHONY: dataset-list
dataset-list:
	./scripts/load_dataset.sh list

.PHONY: dataset-show
dataset-show:
	./scripts/load_dataset.sh show "$(DATASET_SPEC)"

.PHONY: dataset-load
dataset-load: docker-up
	DATASET_SIZE="$(DATASET_SIZE)" DATASET_SEED="$(DATASET_SEED)" DATASET_SCHEMA="$(DATASET_SCHEMA)" ./scripts/load_dataset.sh load "$(DATASET_SPEC)"

.PHONY: experiment-list
experiment-list:
	./scripts/run_experiment.sh list

.PHONY: experiment-show
experiment-show:
	./scripts/run_experiment.sh show "$(EXPERIMENT_SPEC)"

.PHONY: experiment-plan
experiment-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan "$(EXPERIMENT_SPEC)"

.PHONY: experiment-run
experiment-run:
	./scripts/run_experiment.sh run "$(EXPERIMENT_SPEC)"

.PHONY: experiment-report
experiment-report:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report RUN_DIR=runs/<run-id>' >&2; exit 2; }
	./scripts/report_run.sh "$(RUN_DIR)"

.PHONY: experiment-verify
experiment-verify:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-verify RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify "$(RUN_DIR)"

.PHONY: experiment-report-go
experiment-report-go:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report-go RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report run "$(RUN_DIR)"

.PHONY: experiment-summary
experiment-summary:
	@test -n "$(SUMMARY_INPUT)" || { echo 'Usage: make experiment-summary SUMMARY_INPUT=runs/repeats/<id>' >&2; exit 2; }
	@if [[ -n "$(SUMMARY_OUT)" ]]; then \
		./scripts/summarize_runs.sh --output "$(SUMMARY_OUT)" "$(SUMMARY_INPUT)"; \
	else \
		./scripts/summarize_runs.sh "$(SUMMARY_INPUT)"; \
	fi

.PHONY: experiment-summary-go
experiment-summary-go:
	@test -n "$(SUMMARY_INPUT)" || { echo 'Usage: make experiment-summary-go SUMMARY_INPUT=runs/repeats/<id>' >&2; exit 2; }
	@if [[ -n "$(SUMMARY_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report summary --output "$(SUMMARY_OUT)" "$(SUMMARY_INPUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report summary "$(SUMMARY_INPUT)"; \
	fi

.PHONY: experiment-history
experiment-history:
	@test -n "$(HISTORY_INPUTS)" || { echo 'Usage: make experiment-history HISTORY_INPUTS="runs/repeats/a runs/repeats/b"' >&2; exit 2; }
	@if [[ -n "$(HISTORY_OUT)" ]]; then \
		./scripts/compare_run_history.sh --output "$(HISTORY_OUT)" $(HISTORY_INPUTS); \
	else \
		./scripts/compare_run_history.sh $(HISTORY_INPUTS); \
	fi

.PHONY: experiment-history-go
experiment-history-go:
	@test -n "$(HISTORY_INPUTS)" || { echo 'Usage: make experiment-history-go HISTORY_INPUTS="runs/repeats/a runs/repeats/b"' >&2; exit 2; }
	@if [[ -n "$(HISTORY_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report history --output "$(HISTORY_OUT)" $(HISTORY_INPUTS); \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report history $(HISTORY_INPUTS); \
	fi

.PHONY: experiment-repeat
experiment-repeat:
	EXPERIMENT_REPEAT_COUNT="$(EXPERIMENT_REPEAT_COUNT)" EXPERIMENT_REPEAT_ID="$(EXPERIMENT_REPEAT_ID)" ./scripts/run_experiment_repeated.sh "$(EXPERIMENT_SPEC)"

.PHONY: experiment-compare
experiment-compare:
	@test -n "$(BASELINE_RUN)" || { echo 'Usage: make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	@test -n "$(CANDIDATE_RUN)" || { echo 'Usage: make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	./scripts/compare_runs.sh "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

.PHONY: experiment-compare-go
experiment-compare-go:
	@test -n "$(BASELINE_RUN)" || { echo 'Usage: make experiment-compare-go BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	@test -n "$(CANDIDATE_RUN)" || { echo 'Usage: make experiment-compare-go BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report compare "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

.PHONY: matrix-list
matrix-list:
	./scripts/run_experiment_matrix.sh list

.PHONY: matrix-show
matrix-show:
	./scripts/run_experiment_matrix.sh show "$(MATRIX_SPEC)"

.PHONY: matrix-plan
matrix-plan:
	./scripts/run_experiment_matrix.sh plan "$(MATRIX_SPEC)"

.PHONY: matrix-run
matrix-run:
	./scripts/run_experiment_matrix.sh run "$(MATRIX_SPEC)"

.PHONY: spec-list
spec-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec list "$(SPEC_KIND)"

.PHONY: spec-show
spec-show:
	@test -n "$(SPEC_ID)" || { echo 'Usage: make spec-show SPEC_KIND=workload SPEC_ID=pgbench/tiny' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec show "$(SPEC_KIND)" "$(SPEC_ID)"

.PHONY: spec-reference
spec-reference:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec reference "$(SPEC_KIND)"

.PHONY: spec-schema
spec-schema:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec schema "$(SPEC_KIND)"

.PHONY: spec-docs-check
spec-docs-check:
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" ./tests/spec_docs.sh

.PHONY: spec-validate
spec-validate:
	@if [[ -n "$(SPEC_ID)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec validate "$(SPEC_KIND)" "$(SPEC_ID)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec validate; \
	fi

.PHONY: workload-show
workload-show:
	./scripts/run_workload.sh show "$(WORKLOAD_SPEC)"

.PHONY: workload-run
workload-run:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_workload.sh run "$(WORKLOAD_SPEC)"

.PHONY: scan-artifacts
scan-artifacts:
	./scripts/scan_pg_failures.sh $(SCAN_PATHS)

.PHONY: scan-artifacts-go
scan-artifacts-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench scan failures $(SCAN_PATHS)

.PHONY: privacy-scan
privacy-scan:
	@tmp="$$(mktemp "$${TMPDIR:-/tmp}/postgres-experiment-workbench-privacy.XXXXXX")"; \
	home_pattern="$$(printf '%s' "$$HOME" | sed 's/[][\\.^$$*+?{}|()]/\\&/g')"; \
	pattern="$$(printf '%s|%s|%s|%s' 'gh''o_' 'gh''p_' 'to''ken' 'se''cret')"; \
	if [[ -n "$$home_pattern" ]]; then pattern="$$pattern|$$home_pattern"; fi; \
	rg -n -i "$$pattern" . -g '!notes/**' -g '!logs/**' -g '!runs/**' -g '!generated/**' -g '!.tmp/**' -g '!.git/**' > "$$tmp" 2>&1; \
	status="$$?"; \
	if [[ "$$status" = "1" ]]; then \
		rm -f "$$tmp"; \
		echo 'PASS: privacy scan'; \
	elif [[ "$$status" = "0" ]]; then \
		cat "$$tmp"; \
		rm -f "$$tmp"; \
		echo 'FAIL: privacy scan found sensitive-looking public text' >&2; \
		exit 1; \
	else \
		cat "$$tmp"; \
		rm -f "$$tmp"; \
		echo 'FAIL: privacy scan command failed' >&2; \
		exit "$$status"; \
	fi

.PHONY: workload-start
workload-start: docker-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/workload_bg.sh start-profile "$(PROFILE)" "$(WORKLOAD_SQL)"

.PHONY: workload-start-spec
workload-start-spec:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/workload_bg.sh start-spec "$(WORKLOAD_SPEC)"

.PHONY: workload-start-sql
workload-start-sql: docker-up
	@test -n "$(SQL)" || { echo 'Usage: make workload-start-sql SQL=path/to/file.sql' >&2; exit 2; }
	./scripts/workload_bg.sh start-sql "$(SQL)"

.PHONY: workload-start-noisia
workload-start-noisia: docker-up
	NOISIA_DURATION="$(NOISIA_DURATION)" NOISIA_JOBS="$(NOISIA_JOBS)" ./scripts/workload_bg.sh start-noisia "$(WORKLOAD)"

.PHONY: workload-status
workload-status:
	./scripts/workload_bg.sh status

.PHONY: workload-log
workload-log:
	./scripts/workload_bg.sh log

.PHONY: workload-wait
workload-wait:
	./scripts/workload_bg.sh wait

.PHONY: workload-stop
workload-stop:
	./scripts/workload_bg.sh stop

.PHONY: run-sql
run-sql: docker-up
	@test -n "$(SQL)" || { echo 'Usage: make run-sql SQL=path/to/file.sql' >&2; exit 2; }
	./scripts/run_sql_logged.sh "$(SQL)"

.PHONY: noisia-help
noisia-help:
	./scripts/run_noisia.sh help

.PHONY: noisia-wait
noisia-wait:
	NOISIA_DURATION="$(NOISIA_DURATION)" NOISIA_JOBS="$(NOISIA_JOBS)" ./scripts/run_noisia.sh wait-xacts

.PHONY: noisia-temp
noisia-temp:
	NOISIA_DURATION="$(NOISIA_DURATION)" NOISIA_JOBS="$(NOISIA_JOBS)" ./scripts/run_noisia.sh temp-files

.PHONY: go-test
go-test:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) test ./...

.PHONY: pgworkbench
pgworkbench:
	mkdir -p generated/bin
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) build -trimpath -ldflags '$(PGWORKBENCH_LDFLAGS)' -o generated/bin/pgworkbench ./cmd/pgworkbench

.PHONY: release-snapshot
release-snapshot:
	mkdir -p "$(RELEASE_DIR)"
	@for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}"; \
		out_dir="$(RELEASE_DIR)/$${name}"; \
		rm -rf "$$out_dir"; \
		mkdir -p "$$out_dir"; \
		echo "building $$name"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) build -trimpath -ldflags '$(PGWORKBENCH_LDFLAGS)' -o "$$out_dir/pgworkbench" ./cmd/pgworkbench; \
		tar -C "$$out_dir" -czf "$(RELEASE_DIR)/$${name}.tar.gz" pgworkbench; \
	done
	@rm -f "$(RELEASE_CHECKSUM_FILE)"
	@for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}.tar.gz"; \
		if command -v sha256sum >/dev/null 2>&1; then \
			(cd "$(RELEASE_DIR)" && sha256sum "$$name") >> "$(RELEASE_CHECKSUM_FILE)"; \
		else \
			(cd "$(RELEASE_DIR)" && shasum -a 256 "$$name") >> "$(RELEASE_CHECKSUM_FILE)"; \
		fi; \
	done
	@for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}"; \
		printf '%s\n' "$(RELEASE_DIR)/$${name}.tar.gz"; \
	done
	@printf '%s\n' "$(RELEASE_CHECKSUM_FILE)"

.PHONY: check
check:
	bash -n scripts/*.sh tests/*.sh
	git diff --check
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) test ./...
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench patchset validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench source plan pg-source/check >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology inspect single >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec reference all >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec schema all >/dev/null
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" ./tests/spec_docs.sh
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench scan failures $(SCAN_PATHS) >/dev/null
	./tests/profile_catalog.sh
	./tests/patchsets.sh
	./tests/shell_portability.sh
	./tests/scan_failures.sh
	./tests/run_verify.sh
	./tests/report_runs.sh
	./tests/summarize_runs.sh
	./tests/compare_runs.sh
	./tests/history.sh

.PHONY: test
test: docker-up
	./tests/smoke.sh
	./tests/profile_catalog.sh
	./tests/profiles.sh
	./tests/datasets.sh
	./tests/workloads.sh
	./tests/scan_failures.sh
	./tests/topologies.sh
	./tests/experiments.sh
	./tests/report_runs.sh
	./tests/summarize_runs.sh
	./tests/compare_runs.sh
	./tests/history.sh
	./tests/matrices.sh

.PHONY: release-check
release-check: doctor check quickstart test scan-artifacts scan-artifacts-go pgworkbench privacy-scan
	@echo 'PASS: release-check'
