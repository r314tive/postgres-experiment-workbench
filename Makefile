SHELL := /usr/bin/env bash

COMPOSE ?= docker compose
ENV_FILE ?= $(if $(wildcard .env),.env,.env.example)
PROFILE ?= smoke
PROFILE_SIZE ?= small
PROFILE_SECONDS ?= 30
WORKLOAD_SQL ?= 10_run.sql
WORKLOAD ?= wait-xacts
WORKLOAD_SPEC ?= workloads/sql/smoke-run.env
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
	@printf '  %-24s %s\n' 'make psql' 'Open psql'
	@printf '  %-24s %s\n' 'make profile-setup' 'Run profiles/$(PROFILE)/sql/00_setup.sql'
	@printf '  %-24s %s\n' 'make profile-run' 'Run profiles/$(PROFILE)/sql/$(WORKLOAD_SQL)'
	@printf '  %-24s %s\n' 'make profile-reset' 'Run setup and run SQL for PROFILE'
	@printf '  %-24s %s\n' 'make monitor' 'Show basic PostgreSQL activity/statistics'
	@printf '  %-24s %s\n' 'make metrics-sample' 'Sample generic PostgreSQL metrics to CSV'
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
	$(COMPOSE) --env-file $(ENV_FILE) up -d postgres
	./scripts/wait_for_pg.sh

.PHONY: docker-down
docker-down:
	$(COMPOSE) --env-file $(ENV_FILE) down

.PHONY: docker-reset
docker-reset:
	$(COMPOSE) --env-file $(ENV_FILE) down -v
	$(COMPOSE) --env-file $(ENV_FILE) up -d postgres
	./scripts/wait_for_pg.sh

.PHONY: psql
psql: docker-up
	./scripts/psql.sh

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

.PHONY: workload-show
workload-show:
	./scripts/run_workload.sh show "$(WORKLOAD_SPEC)"

.PHONY: workload-run
workload-run: docker-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_workload.sh run "$(WORKLOAD_SPEC)"

.PHONY: workload-start
workload-start: docker-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/workload_bg.sh start-profile "$(PROFILE)" "$(WORKLOAD_SQL)"

.PHONY: workload-start-spec
workload-start-spec: docker-up
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
	./tests/workloads.sh
