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
DATASET_SPEC ?= synthetic/items
DATASET_SIZE ?= small
DATASET_SEED ?= 1
DATASET_SCHEMA ?= dataset_synthetic
BASELINE_RUN ?=
CANDIDATE_RUN ?=
RUN_DIR ?=
SUMMARY_INPUT ?=
SUMMARY_OUT ?=
SCAN_PATHS ?= logs generated
METRICS_INTERVAL ?= 1
METRICS_DURATION ?= 30
METRICS_SAMPLES ?=
METRICS_OUT ?=
NOISIA_DURATION ?= 60
NOISIA_JOBS ?= 2

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf '%s\n' 'Targets:'
	@printf '  %-24s %s\n' 'make docker-up' 'Start PostgreSQL workbench'
	@printf '  %-24s %s\n' 'make docker-down' 'Stop PostgreSQL workbench, keep volume'
	@printf '  %-24s %s\n' 'make docker-reset' 'Recreate PostgreSQL volume'
	@printf '  %-24s %s\n' 'make topology-list' 'List topology specs'
	@printf '  %-24s %s\n' 'make topology-show' 'Show TOPOLOGY'
	@printf '  %-24s %s\n' 'make topology-up' 'Start TOPOLOGY'
	@printf '  %-24s %s\n' 'make topology-reset' 'Recreate TOPOLOGY volumes'
	@printf '  %-24s %s\n' 'make topology-status' 'Show TOPOLOGY runtime status'
	@printf '  %-24s %s\n' 'make topology-down' 'Stop TOPOLOGY'
	@printf '  %-24s %s\n' 'make psql' 'Open psql'
	@printf '  %-24s %s\n' 'make pg-config-apply' 'Apply PG_CONFIG to disposable PostgreSQL'
	@printf '  %-24s %s\n' 'make snapshot' 'Capture PostgreSQL snapshot artifacts'
	@printf '  %-24s %s\n' 'make profile-setup' 'Run profiles/$(PROFILE)/sql/00_setup.sql'
	@printf '  %-24s %s\n' 'make profile-run' 'Run profiles/$(PROFILE)/sql/$(WORKLOAD_SQL)'
	@printf '  %-24s %s\n' 'make profile-reset' 'Run setup and run SQL for PROFILE'
	@printf '  %-24s %s\n' 'make monitor' 'Show basic PostgreSQL activity/statistics'
	@printf '  %-24s %s\n' 'make metrics-sample' 'Sample generic PostgreSQL metrics to CSV'
	@printf '  %-24s %s\n' 'make scan-artifacts' 'Scan logs/artifacts for PG failure evidence'
	@printf '  %-24s %s\n' 'make dataset-list' 'List dataset specs'
	@printf '  %-24s %s\n' 'make dataset-show' 'Show DATASET_SPEC'
	@printf '  %-24s %s\n' 'make dataset-load' 'Load DATASET_SPEC'
	@printf '  %-24s %s\n' 'make experiment-list' 'List experiment specs'
	@printf '  %-24s %s\n' 'make experiment-show' 'Show EXPERIMENT_SPEC'
	@printf '  %-24s %s\n' 'make experiment-run' 'Run EXPERIMENT_SPEC into runs/<run-id>'
	@printf '  %-24s %s\n' 'make experiment-report' 'Render Markdown report for RUN_DIR'
	@printf '  %-24s %s\n' 'make experiment-summary' 'Summarize a repeat/matrix/run series'
	@printf '  %-24s %s\n' 'make experiment-repeat' 'Run EXPERIMENT_SPEC repeatedly'
	@printf '  %-24s %s\n' 'make experiment-compare' 'Compare BASELINE_RUN and CANDIDATE_RUN'
	@printf '  %-24s %s\n' 'make matrix-list' 'List experiment matrix specs'
	@printf '  %-24s %s\n' 'make matrix-show' 'Show MATRIX_SPEC'
	@printf '  %-24s %s\n' 'make matrix-plan' 'Preview MATRIX_SPEC combinations'
	@printf '  %-24s %s\n' 'make matrix-run' 'Run MATRIX_SPEC combinations'
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
	@printf '  %-24s %s\n' 'make test' 'Run profile and workload verification'

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

.PHONY: topology-list
topology-list:
	./scripts/topology.sh list

.PHONY: topology-show
topology-show:
	./scripts/topology.sh show "$(TOPOLOGY)"

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

.PHONY: experiment-run
experiment-run:
	./scripts/run_experiment.sh run "$(EXPERIMENT_SPEC)"

.PHONY: experiment-report
experiment-report:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report RUN_DIR=runs/<run-id>' >&2; exit 2; }
	./scripts/report_run.sh "$(RUN_DIR)"

.PHONY: experiment-summary
experiment-summary:
	@test -n "$(SUMMARY_INPUT)" || { echo 'Usage: make experiment-summary SUMMARY_INPUT=runs/repeats/<id>' >&2; exit 2; }
	@if [[ -n "$(SUMMARY_OUT)" ]]; then \
		./scripts/summarize_runs.sh --output "$(SUMMARY_OUT)" "$(SUMMARY_INPUT)"; \
	else \
		./scripts/summarize_runs.sh "$(SUMMARY_INPUT)"; \
	fi

.PHONY: experiment-repeat
experiment-repeat:
	EXPERIMENT_REPEAT_COUNT="$(EXPERIMENT_REPEAT_COUNT)" EXPERIMENT_REPEAT_ID="$(EXPERIMENT_REPEAT_ID)" ./scripts/run_experiment_repeated.sh "$(EXPERIMENT_SPEC)"

.PHONY: experiment-compare
experiment-compare:
	@test -n "$(BASELINE_RUN)" || { echo 'Usage: make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	@test -n "$(CANDIDATE_RUN)" || { echo 'Usage: make experiment-compare BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	./scripts/compare_runs.sh "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

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

.PHONY: workload-show
workload-show:
	./scripts/run_workload.sh show "$(WORKLOAD_SPEC)"

.PHONY: workload-run
workload-run:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_workload.sh run "$(WORKLOAD_SPEC)"

.PHONY: scan-artifacts
scan-artifacts:
	./scripts/scan_pg_failures.sh $(SCAN_PATHS)

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

.PHONY: test
test: docker-up
	./tests/smoke.sh
	./tests/profiles.sh
	./tests/datasets.sh
	./tests/workloads.sh
	./tests/scan_failures.sh
	./tests/topologies.sh
	./tests/experiments.sh
	./tests/report_runs.sh
	./tests/summarize_runs.sh
	./tests/compare_runs.sh
	./tests/matrices.sh
