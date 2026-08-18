SHELL := /usr/bin/env bash

COMPOSE ?= docker compose
PGWORKBENCH_RUNTIME ?= docker
ENV_FILE ?= $(if $(wildcard .env),.env,.env.example)
PROFILE ?= smoke
PROFILE_SIZE ?= small
PROFILE_SECONDS ?= 30
PROFILE_PLAN_SQL ?=
PG_CONFIG ?= default
TOPOLOGY ?= single
WORKLOAD_SQL ?= 10_run.sql
WORKLOAD ?= wait-xacts
WORKLOAD_SPEC ?= workloads/sql/smoke-run.env
UTILITY_TEST_SPEC ?= pg-dump/smoke
UTILITY_TEST_RUN_ID ?=
UTILITY_RUN_ID_ARG = $(if $(strip $(UTILITY_TEST_RUN_ID)),--run-id "$(UTILITY_TEST_RUN_ID)",)
UTILITY_SUITE ?= native-dump
UTILITY_SUITE_RUN ?=
UTILITY_SUITE_RUN_INPUTS ?=
UTILITY_SUITE_BUNDLE_OUT ?=
EXPERIMENT_SPEC ?= smoke
EXPERIMENT_REPEAT_COUNT ?= 3
EXPERIMENT_REPEAT_ID ?=
BENCHMARK_SPEC ?= pgbench/smoke
BENCHMARK_RUN_ID ?=
BENCHMARK_RUN_ID_ARG = $(if $(strip $(BENCHMARK_RUN_ID)),--run-id "$(BENCHMARK_RUN_ID)",)
BENCHMARK_SUBJECT ?= default
BENCHMARK_SERIES ?=
BENCHMARK_BASELINE ?=
BENCHMARK_CANDIDATE ?=
BENCHMARK_BUNDLE_OUT ?=
BENCHMARK_HISTORY_ID ?=
BENCHMARK_HISTORY ?=
BENCHMARK_HISTORY_INPUTS ?=
BENCHMARK_HISTORY_BUNDLE_OUT ?=
BENCHMARK_IMPORT ?=
BENCHMARK_IMPORT_BUNDLE_OUT ?=
BENCHMARK_CAMPAIGN_ID ?=
BENCHMARK_CAMPAIGN ?=
BENCHMARK_CAMPAIGN_INPUTS ?=
BENCHMARK_CAMPAIGN_SUBJECT ?= default
BENCHMARK_CAMPAIGN_BUNDLE_OUT ?=
BENCHMARK_AB_BASELINE ?=
BENCHMARK_AB_CANDIDATE ?=
BENCHMARK_AB_RUN_ID ?=
BENCHMARK_AB_RUN ?=
BENCHMARK_AB_OPTIONS ?=
BENCHMARK_AB_BUNDLE_OUT ?=
BENCHMARK_DRIVER_ID ?=
BENCHMARK_DRIVER_RUNTIME_ROOT ?=
BENCHMARK_DRIVER_BINARY ?=
BENCHMARK_DRIVER_CONFIG ?=
BENCHMARK_DRIVER_SCRIPT ?=
BENCHMARK_DRIVER_WORKLOAD ?=
BENCHMARK_DRIVER_TIMEOUT ?= 1h
BENCHMARK_DRIVER_OUTPUT ?=
BENCHMARK_DRIVER_EXECUTION ?=
BENCHMARK_DRIVER_ACKNOWLEDGE ?= 0
BENCHMARK_HOST_OUTPUT ?= generated/host-qualification.json
BENCHMARK_HOST_INPUT ?=
BENCHMARK_HOST_OPTIONS ?=
OPERATION_BENCHMARK_SPEC ?= maintenance/vacuum-bloat-manual
OPERATION_BENCHMARK_RUN_ID ?=
OPERATION_BENCHMARK_RUN_ID_ARG = $(if $(strip $(OPERATION_BENCHMARK_RUN_ID)),--run-id "$(OPERATION_BENCHMARK_RUN_ID)",)
OPERATION_BENCHMARK_SERIES ?=
OPERATION_BENCHMARK_BUNDLE_OUT ?=
PGDRILL_SOURCE ?=
PGDRILL_BASELINE ?=
PGDRILL_PREDICATE_FILE ?=
PGDRILL_REQUIRE_BUNDLE ?= 1
MATRIX_SPEC ?= smoke
MATRIX_RUN ?=
MATRIX_EXPECTED_RUNS ?=
PATCHSET ?= chaos/master
SOURCE_WORKLOAD_SPEC ?= pg-source/check
SOURCE_CHECK_PATH ?= generated/pg-source
DATASET_SPEC ?= synthetic/items
DATASET_SIZE ?= small
DATASET_SEED ?= 1
DATASET_SCHEMA ?= dataset_synthetic
DIAGNOSTIC ?= activity
BASELINE_RUN ?=
CANDIDATE_RUN ?=
RUN_DIR ?=
RUN_INPUTS ?=
RUN_STATUS ?=
RUN_LIMIT ?=
RUN_LIST_ARGS = $(if $(RUN_STATUS),--status $(RUN_STATUS),) $(if $(RUN_LIMIT),--limit $(RUN_LIMIT),) $(RUN_INPUTS)
RUN_BUNDLE_OUT ?=
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
METRICS_APPEND ?= 0
NOISIA_DURATION ?= 60
NOISIA_JOBS ?= 2
GO ?= go
GO_CACHE ?= $(CURDIR)/.tmp/go-cache
GO_MOD_CACHE ?= $(CURDIR)/.tmp/go-mod-cache
VERSION ?= 0.0.0-dev
BUILD_COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct HEAD 2>/dev/null || echo 0)
BUILD_DATE ?= $(shell git show -s --format=%cI HEAD 2>/dev/null || echo 1970-01-01T00:00:00Z)
RELEASE_DIR ?= generated/release
RELEASE_PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64
RELEASE_CHECKSUM_FILE ?= $(RELEASE_DIR)/pgworkbench-$(VERSION)-SHA256SUMS.txt
RELEASE_MANIFEST_FILE ?= $(RELEASE_DIR)/pgworkbench-$(VERSION)-release-manifest.json
PGWORKBENCH_LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.builtAt=$(BUILD_DATE)
PGWORKBENCH_CLI ?= $(GO) run ./cmd/pgworkbench

.DEFAULT_GOAL := help

.PHONY: help
help:
	@printf '%s\n' 'Targets:'
	@printf '  %-24s %s\n' 'make runtime-up' 'Start selected PGWORKBENCH_RUNTIME'
	@printf '  %-24s %s\n' 'make runtime-reset' 'Reset selected disposable runtime'
	@printf '  %-24s %s\n' 'make runtime-down' 'Stop selected runtime'
	@printf '  %-24s %s\n' 'make docker-up' 'Start PostgreSQL workbench'
	@printf '  %-24s %s\n' 'make docker-down' 'Stop PostgreSQL workbench, keep volume'
	@printf '  %-24s %s\n' 'make docker-reset' 'Recreate PostgreSQL volume'
	@printf '  %-24s %s\n' 'make quickstart-plan' 'Preview the smoke experiment quickstart'
	@printf '  %-24s %s\n' 'make quickstart' 'Run smoke quickstart and write report.md'
	@printf '  %-24s %s\n' 'make doctor' 'Check local workbench prerequisites'
	@printf '  %-24s %s\n' 'make compatibility' 'Render declared candidate support cells'
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
	@printf '  %-24s %s\n' 'make profile-plan' 'Preview PROFILE SQL steps without psql'
	@printf '  %-24s %s\n' 'make profile-plan-json' 'Preview PROFILE SQL steps as JSON'
	@printf '  %-24s %s\n' 'make patchset-list' 'List PostgreSQL source patchsets'
	@printf '  %-24s %s\n' 'make patchset-show' 'Show PATCHSET metadata'
	@printf '  %-24s %s\n' 'make patchset-validate' 'Validate patchset metadata and files'
	@printf '  %-24s %s\n' 'make source-plan' 'Preview PostgreSQL source-check plan'
	@printf '  %-24s %s\n' 'make source-classify' 'Classify PostgreSQL source-check artifacts'
	@printf '  %-24s %s\n' 'make profile-setup' 'Run profiles/$(PROFILE)/sql/00_setup.sql'
	@printf '  %-24s %s\n' 'make profile-run' 'Run profiles/$(PROFILE)/sql/$(WORKLOAD_SQL)'
	@printf '  %-24s %s\n' 'make profile-reset' 'Run setup and run SQL for PROFILE'
	@printf '  %-24s %s\n' 'make monitor' 'Show basic PostgreSQL activity/statistics'
	@printf '  %-24s %s\n' 'make metrics-plan' 'Preview metrics sampler CSV contract'
	@printf '  %-24s %s\n' 'make metrics-plan-json' 'Preview metrics sampler CSV contract as JSON'
	@printf '  %-24s %s\n' 'make metrics-sample' 'Sample generic PostgreSQL metrics to CSV'
	@printf '  %-24s %s\n' 'make diagnostics-list' 'List read-only diagnostic SQL snippets'
	@printf '  %-24s %s\n' 'make diagnostics-show' 'Show DIAGNOSTIC SQL'
	@printf '  %-24s %s\n' 'make diagnostics-run' 'Run DIAGNOSTIC SQL against local PostgreSQL'
	@printf '  %-24s %s\n' 'make scan-artifacts' 'Scan logs/artifacts for PG failure evidence'
	@printf '  %-24s %s\n' 'make scan-artifacts-go' 'Scan logs/artifacts with Go CLI'
	@printf '  %-24s %s\n' 'make privacy-scan' 'Scan public files for sensitive-looking text'
	@printf '  %-24s %s\n' 'make dataset-list' 'List dataset specs'
	@printf '  %-24s %s\n' 'make dataset-show' 'Show DATASET_SPEC'
	@printf '  %-24s %s\n' 'make dataset-plan' 'Preview DATASET_SPEC load plan'
	@printf '  %-24s %s\n' 'make dataset-list-go' 'List dataset specs with Go CLI'
	@printf '  %-24s %s\n' 'make dataset-show-go' 'Show DATASET_SPEC with Go CLI'
	@printf '  %-24s %s\n' 'make dataset-plan-go' 'Preview DATASET_SPEC load plan with Go CLI'
	@printf '  %-24s %s\n' 'make dataset-plan-json' 'Preview DATASET_SPEC load plan as JSON'
	@printf '  %-24s %s\n' 'make dataset-load' 'Load DATASET_SPEC'
	@printf '  %-24s %s\n' 'make experiment-list' 'List experiment specs'
	@printf '  %-24s %s\n' 'make experiment-show' 'Show EXPERIMENT_SPEC'
	@printf '  %-24s %s\n' 'make experiment-plan' 'Render EXPERIMENT_SPEC execution plan'
	@printf '  %-24s %s\n' 'make experiment-plan-json' 'Render EXPERIMENT_SPEC plan as JSON'
	@printf '  %-24s %s\n' 'make experiment-plan-expanded' 'Render expanded EXPERIMENT_SPEC plan'
	@printf '  %-24s %s\n' 'make experiment-plan-expanded-json' 'Render expanded EXPERIMENT_SPEC plan as JSON'
	@printf '  %-24s %s\n' 'make experiment-run' 'Run EXPERIMENT_SPEC into runs/<run-id>'
	@printf '  %-24s %s\n' 'make run-list' 'List experiment run artifacts'
	@printf '  %-24s %s\n' 'make run-list-json' 'List experiment run artifacts as JSON'
	@printf '  %-24s %s\n' 'make run-show' 'Show RUN_DIR artifact summary'
	@printf '  %-24s %s\n' 'make run-show-json' 'Show RUN_DIR artifact summary as JSON'
	@printf '  %-24s %s\n' 'make run-bundle' 'Bundle RUN_DIR artifact into tar.gz'
	@printf '  %-24s %s\n' 'make run-bundle-json' 'Bundle RUN_DIR artifact and print JSON metadata'
	@printf '  %-24s %s\n' 'make experiment-verify-bundle' 'Verify extracted RUN_DIR with required inventory'
	@printf '  %-24s %s\n' 'make experiment-verify' 'Verify RUN_DIR artifact integrity'
	@printf '  %-24s %s\n' 'make experiment-verify-json' 'Verify RUN_DIR artifact integrity as JSON'
	@printf '  %-24s %s\n' 'make experiment-report' 'Render Markdown report with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-report-shell' 'Render Markdown report with shell script'
	@printf '  %-24s %s\n' 'make experiment-summary' 'Summarize runs with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-summary-shell' 'Summarize runs with shell script'
	@printf '  %-24s %s\n' 'make experiment-history' 'Compare run history with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-history-shell' 'Compare run history with shell script'
	@printf '  %-24s %s\n' 'make experiment-repeat' 'Run EXPERIMENT_SPEC repeatedly'
	@printf '  %-24s %s\n' 'make experiment-compare' 'Compare runs with Go CLI'
	@printf '  %-24s %s\n' 'make experiment-compare-shell' 'Compare runs with shell script'
	@printf '  %-24s %s\n' 'make benchmark-list' 'List benchmark specs'
	@printf '  %-24s %s\n' 'make benchmark-show' 'Show BENCHMARK_SPEC'
	@printf '  %-24s %s\n' 'make benchmark-validate' 'Validate all benchmark specs'
	@printf '  %-24s %s\n' 'make benchmark-plan' 'Render BENCHMARK_SPEC protocol plan'
	@printf '  %-24s %s\n' 'make benchmark-plan-json' 'Render BENCHMARK_SPEC protocol plan as JSON'
	@printf '  %-24s %s\n' 'make benchmark-drivers' 'Show exact pinned external benchmark drivers'
	@printf '  %-24s %s\n' 'make benchmark-driver-show' 'Show BENCHMARK_DRIVER_ID pin'
	@printf '  %-24s %s\n' 'make benchmark-driver-run' 'Run a pinned driver with explicit disposable-target ack'
	@printf '  %-24s %s\n' 'make benchmark-driver-verify' 'Verify BENCHMARK_DRIVER_EXECUTION'
	@printf '  %-24s %s\n' 'make benchmark-host-inspect' 'Record strict BENCHMARK_HOST_OPTIONS qualification'
	@printf '  %-24s %s\n' 'make benchmark-host-verify' 'Verify BENCHMARK_HOST_INPUT qualification'
	@printf '  %-24s %s\n' 'make operation-list' 'List descriptive operation benchmark specs'
	@printf '  %-24s %s\n' 'make operation-show' 'Show OPERATION_BENCHMARK_SPEC'
	@printf '  %-24s %s\n' 'make operation-run' 'Run OPERATION_BENCHMARK_SPEC'
	@printf '  %-24s %s\n' 'make operation-run-show' 'Show OPERATION_BENCHMARK_SERIES artifact'
	@printf '  %-24s %s\n' 'make operation-verify' 'Verify OPERATION_BENCHMARK_SERIES artifact'
	@printf '  %-24s %s\n' 'make operation-verify-bundle' 'Verify an extracted operation bundle'
	@printf '  %-24s %s\n' 'make operation-bundle' 'Bundle operation series and linked runs'
	@printf '  %-24s %s\n' 'make benchmark-run' 'Run BENCHMARK_SPEC on PGWORKBENCH_RUNTIME'
	@printf '  %-24s %s\n' 'make benchmark-run-json' 'Run BENCHMARK_SPEC and print JSON result'
	@printf '  %-24s %s\n' 'make benchmark-run-show' 'Show BENCHMARK_SERIES artifact'
	@printf '  %-24s %s\n' 'make benchmark-run-verify' 'Verify BENCHMARK_SERIES artifact'
	@printf '  %-24s %s\n' 'make benchmark-run-verify-bundle' 'Verify an extracted benchmark bundle'
	@printf '  %-24s %s\n' 'make benchmark-run-bundle' 'Bundle BENCHMARK_SERIES and linked runs'
	@printf '  %-24s %s\n' 'make benchmark-compare' 'Compare BENCHMARK_BASELINE and BENCHMARK_CANDIDATE'
	@printf '  %-24s %s\n' 'make benchmark-history-create' 'Create bounded history from BENCHMARK_HISTORY_INPUTS'
	@printf '  %-24s %s\n' 'make benchmark-history-show' 'Show BENCHMARK_HISTORY artifact'
	@printf '  %-24s %s\n' 'make benchmark-history-verify' 'Verify BENCHMARK_HISTORY artifact'
	@printf '  %-24s %s\n' 'make benchmark-history-verify-bundle' 'Verify an extracted history bundle'
	@printf '  %-24s %s\n' 'make benchmark-history-bundle' 'Bundle BENCHMARK_HISTORY and transitive evidence'
	@printf '  %-24s %s\n' 'make benchmark-import-verify' 'Verify BENCHMARK_IMPORT artifact'
	@printf '  %-24s %s\n' 'make benchmark-import-verify-bundle' 'Verify an extracted import bundle'
	@printf '  %-24s %s\n' 'make benchmark-import-bundle' 'Bundle BENCHMARK_IMPORT and raw evidence'
	@printf '  %-24s %s\n' 'make benchmark-campaign-run' 'Run ordered BENCHMARK_CAMPAIGN_INPUTS'
	@printf '  %-24s %s\n' 'make benchmark-campaign-show' 'Show BENCHMARK_CAMPAIGN artifact'
	@printf '  %-24s %s\n' 'make benchmark-campaign-verify' 'Verify BENCHMARK_CAMPAIGN artifact'
	@printf '  %-24s %s\n' 'make benchmark-campaign-verify-bundle' 'Verify an extracted campaign bundle'
	@printf '  %-24s %s\n' 'make benchmark-campaign-bundle' 'Bundle BENCHMARK_CAMPAIGN and transitive evidence'
	@printf '  %-24s %s\n' 'make benchmark-ab-run' 'Run qualified counterbalanced A/B protocol'
	@printf '  %-24s %s\n' 'make benchmark-ab-show' 'Show BENCHMARK_AB_RUN artifact'
	@printf '  %-24s %s\n' 'make benchmark-ab-verify' 'Verify BENCHMARK_AB_RUN artifact'
	@printf '  %-24s %s\n' 'make benchmark-ab-verify-bundle' 'Verify an extracted A/B bundle'
	@printf '  %-24s %s\n' 'make benchmark-ab-bundle' 'Bundle BENCHMARK_AB_RUN and transitive evidence'
	@printf '  %-24s %s\n' 'make pgdrill-baseline-export' 'Export bounded provenance from PGDRILL_SOURCE'
	@printf '  %-24s %s\n' 'make pgdrill-baseline-verify' 'Verify PGDRILL_BASELINE, optionally against source'
	@printf '  %-24s %s\n' 'make matrix-list' 'List experiment matrix specs'
	@printf '  %-24s %s\n' 'make matrix-show' 'Show MATRIX_SPEC'
	@printf '  %-24s %s\n' 'make matrix-plan' 'Preview MATRIX_SPEC combinations'
	@printf '  %-24s %s\n' 'make matrix-plan-go' 'Preview MATRIX_SPEC combinations with Go CLI'
	@printf '  %-24s %s\n' 'make matrix-plan-json' 'Render MATRIX_SPEC plan as JSON'
	@printf '  %-24s %s\n' 'make matrix-run' 'Run MATRIX_SPEC combinations'
	@printf '  %-24s %s\n' 'make matrix-candidate-verify' 'Verify every run in an exact candidate matrix'
	@printf '  %-24s %s\n' 'make spec-list' 'List SPEC_KIND specs with Go CLI'
	@printf '  %-24s %s\n' 'make spec-show' 'Show SPEC_KIND/SPEC_ID with Go CLI'
	@printf '  %-24s %s\n' 'make spec-reference' 'Render env spec reference with Go CLI'
	@printf '  %-24s %s\n' 'make spec-schema' 'Render env spec JSON Schema with Go CLI'
	@printf '  %-24s %s\n' 'make schema-check' 'Compile Draft 2020-12 schemas and validate fixtures'
	@printf '  %-24s %s\n' 'make spec-docs-check' 'Check tracked env spec docs/schema are current'
	@printf '  %-24s %s\n' 'make spec-validate' 'Validate env specs with Go CLI'
	@printf '  %-24s %s\n' 'make workload-list' 'List workload specs'
	@printf '  %-24s %s\n' 'make workload-show' 'Show WORKLOAD_SPEC'
	@printf '  %-24s %s\n' 'make workload-plan' 'Preview WORKLOAD_SPEC execution plan'
	@printf '  %-24s %s\n' 'make workload-list-go' 'List workload specs with Go CLI'
	@printf '  %-24s %s\n' 'make workload-show-go' 'Show WORKLOAD_SPEC with Go CLI'
	@printf '  %-24s %s\n' 'make workload-plan-go' 'Preview WORKLOAD_SPEC execution plan with Go CLI'
	@printf '  %-24s %s\n' 'make workload-plan-json' 'Preview WORKLOAD_SPEC execution plan as JSON'
	@printf '  %-24s %s\n' 'make workload-run' 'Run WORKLOAD_SPEC with Go result contract'
	@printf '  %-24s %s\n' 'make workload-run-json' 'Run WORKLOAD_SPEC and print JSON result'
	@printf '  %-24s %s\n' 'make workload-run-shell' 'Run WORKLOAD_SPEC with shell runner'
	@printf '  %-24s %s\n' 'make utility-list' 'List utility test specs'
	@printf '  %-24s %s\n' 'make utility-show' 'Show UTILITY_TEST_SPEC'
	@printf '  %-24s %s\n' 'make utility-plan' 'Render UTILITY_TEST_SPEC plan'
	@printf '  %-24s %s\n' 'make utility-plan-json' 'Render UTILITY_TEST_SPEC plan as JSON'
	@printf '  %-24s %s\n' 'make utility-plan-expanded' 'Render expanded UTILITY_TEST_SPEC plan'
	@printf '  %-24s %s\n' 'make utility-run' 'Run UTILITY_TEST_SPEC on PGWORKBENCH_RUNTIME'
	@printf '  %-24s %s\n' 'make utility-run-json' 'Run UTILITY_TEST_SPEC and print JSON result'
	@printf '  %-24s %s\n' 'make utility-suite-list' 'List utility suite specs'
	@printf '  %-24s %s\n' 'make utility-suite-show' 'Show UTILITY_SUITE'
	@printf '  %-24s %s\n' 'make utility-suite-plan' 'Render UTILITY_SUITE batch plan'
	@printf '  %-24s %s\n' 'make utility-suite-plan-json' 'Render UTILITY_SUITE batch plan as JSON'
	@printf '  %-24s %s\n' 'make utility-suite-run' 'Run UTILITY_SUITE through utility runner'
	@printf '  %-24s %s\n' 'make utility-suite-run-json' 'Run UTILITY_SUITE and print JSON result'
	@printf '  %-24s %s\n' 'make utility-suite-run-list' 'List utility suite run artifacts'
	@printf '  %-24s %s\n' 'make utility-suite-run-list-json' 'List utility suite run artifacts as JSON'
	@printf '  %-24s %s\n' 'make utility-suite-run-show' 'Show UTILITY_SUITE_RUN artifact summary'
	@printf '  %-24s %s\n' 'make utility-suite-run-show-json' 'Show UTILITY_SUITE_RUN artifact summary as JSON'
	@printf '  %-24s %s\n' 'make utility-suite-run-bundle' 'Bundle UTILITY_SUITE_RUN artifact into tar.gz'
	@printf '  %-24s %s\n' 'make utility-suite-run-bundle-json' 'Bundle UTILITY_SUITE_RUN artifact and print JSON metadata'
	@printf '  %-24s %s\n' 'make utility-suite-run-verify' 'Verify UTILITY_SUITE_RUN artifact integrity'
	@printf '  %-24s %s\n' 'make utility-suite-run-verify-json' 'Verify UTILITY_SUITE_RUN artifact integrity as JSON'
	@printf '  %-24s %s\n' 'make workload-start' 'Run profile SQL in the background'
	@printf '  %-24s %s\n' 'make workload-start-spec' 'Run WORKLOAD_SPEC in the background'
	@printf '  %-24s %s\n' 'make workload-start-sql' 'Run SQL=path in the background'
	@printf '  %-24s %s\n' 'make workload-start-noisia' 'Run noisia workload in the background'
	@printf '  %-24s %s\n' 'make workload-status' 'Show background workload status'
	@printf '  %-24s %s\n' 'make workload-status-json' 'Show background workload status as JSON'
	@printf '  %-24s %s\n' 'make workload-log' 'Tail background workload log'
	@printf '  %-24s %s\n' 'make workload-stop' 'Stop background workload'
	@printf '  %-24s %s\n' 'make run-sql SQL=path' 'Run a SQL file with logs'
	@printf '  %-24s %s\n' 'make noisia-help' 'Show noisia help'
	@printf '  %-24s %s\n' 'make noisia-wait' 'Run noisia wait transactions'
	@printf '  %-24s %s\n' 'make noisia-temp' 'Run noisia temp files'
	@printf '  %-24s %s\n' 'make go-test' 'Run Go unit tests'
	@printf '  %-24s %s\n' 'make pgworkbench' 'Build Go CLI into generated/bin'
	@printf '  %-24s %s\n' 'make release-snapshot' 'Build pgworkbench release archives'
	@printf '  %-24s %s\n' 'make release-smoke' 'Smoke-test the current-platform standalone archive'
	@printf '  %-24s %s\n' 'make candidate-preflight' 'Require one clean, versioned immutable release candidate'
	@printf '  %-24s %s\n' 'make check' 'Run static and no-Docker checks'
	@printf '  %-24s %s\n' 'make test' 'Run profile and workload verification'
	@printf '  %-24s %s\n' 'make release-check' 'Run release readiness checks'
	@printf '  %-24s %s\n' 'make native-test' 'Run native backend contract tests'

.PHONY: runtime-up
runtime-up:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh up "$(TOPOLOGY)"

.PHONY: runtime-down
runtime-down:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh down "$(TOPOLOGY)"

.PHONY: runtime-reset
runtime-reset:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh reset "$(TOPOLOGY)"
	@PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" ./scripts/apply_pg_config.sh "$(PG_CONFIG)"

.PHONY: runtime-restart
runtime-restart:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh restart "$(TOPOLOGY)"

.PHONY: runtime-status
runtime-status:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh status "$(TOPOLOGY)"

.PHONY: runtime-wait
runtime-wait:
	PGWORKBENCH_RUNTIME="$(PGWORKBENCH_RUNTIME)" COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh wait "$(TOPOLOGY)"

.PHONY: docker-up
docker-up:
	PGWORKBENCH_RUNTIME=docker COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh up "$(TOPOLOGY)"

.PHONY: docker-down
docker-down:
	PGWORKBENCH_RUNTIME=docker COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh down "$(TOPOLOGY)"

.PHONY: docker-reset
docker-reset:
	PGWORKBENCH_RUNTIME=docker COMPOSE="$(COMPOSE)" ENV_FILE="$(ENV_FILE)" ./scripts/runtime.sh reset "$(TOPOLOGY)"
	@PGWORKBENCH_RUNTIME=docker ./scripts/apply_pg_config.sh "$(PG_CONFIG)"

.PHONY: quickstart-plan
quickstart-plan:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan smoke

.PHONY: quickstart
quickstart:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) experiment run --runtime "$(PGWORKBENCH_RUNTIME)" --run-id "$(QUICKSTART_RUN_ID)" smoke
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) run verify "runs/$(QUICKSTART_RUN_ID)"
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) report run "runs/$(QUICKSTART_RUN_ID)" > "runs/$(QUICKSTART_RUN_ID)/report.md"
	@printf 'Quickstart run: %s\n' "runs/$(QUICKSTART_RUN_ID)"
	@printf 'Quickstart report: %s\n' "runs/$(QUICKSTART_RUN_ID)/report.md"

.PHONY: doctor
doctor:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench doctor --runtime "$(PGWORKBENCH_RUNTIME)" $(DOCTOR_FLAGS)

.PHONY: compatibility
compatibility:
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench compatibility show

.PHONY: topology-list
topology-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology list --raw

.PHONY: topology-show
topology-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology show --raw "$(TOPOLOGY)"

.PHONY: topology-inspect
topology-inspect:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology inspect "$(TOPOLOGY)"

.PHONY: topology-ps
topology-ps:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology ps "$(TOPOLOGY)"

.PHONY: topology-up
topology-up:
	$(MAKE) runtime-up

.PHONY: topology-reset
topology-reset:
	$(MAKE) runtime-reset

.PHONY: topology-status
topology-status:
	$(MAKE) runtime-status

.PHONY: topology-down
topology-down:
	$(MAKE) runtime-down

.PHONY: psql
psql: runtime-up
	./scripts/psql.sh

.PHONY: pg-config-apply
pg-config-apply: runtime-up
	./scripts/apply_pg_config.sh "$(PG_CONFIG)"

.PHONY: snapshot
snapshot: runtime-up
	./scripts/snapshot_pg.sh

.PHONY: profile-list
profile-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile list

.PHONY: profile-show
profile-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile show "$(PROFILE)"

.PHONY: profile-validate
profile-validate:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile validate

.PHONY: profile-plan
profile-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile plan --size "$(PROFILE_SIZE)" --seconds "$(PROFILE_SECONDS)" "$(PROFILE)" $(PROFILE_PLAN_SQL)

.PHONY: profile-plan-json
profile-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile plan --json --size "$(PROFILE_SIZE)" --seconds "$(PROFILE_SECONDS)" "$(PROFILE)" $(PROFILE_PLAN_SQL)

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
profile-setup: runtime-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_profile_sql.sh "$(PROFILE)" 00_setup.sql

.PHONY: profile-run
profile-run: runtime-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_profile_sql.sh "$(PROFILE)" "$(WORKLOAD_SQL)"

.PHONY: profile-reset
profile-reset: profile-setup profile-run

.PHONY: monitor
monitor: runtime-up
	./scripts/psql.sh -f sql/monitor.sql

.PHONY: metrics-sample
metrics-sample: runtime-up
	METRICS_INTERVAL="$(METRICS_INTERVAL)" METRICS_DURATION="$(METRICS_DURATION)" METRICS_SAMPLES="$(METRICS_SAMPLES)" METRICS_OUT="$(METRICS_OUT)" METRICS_APPEND="$(METRICS_APPEND)" ./scripts/sample_metrics.sh

.PHONY: metrics-plan
metrics-plan:
	METRICS_INTERVAL="$(METRICS_INTERVAL)" METRICS_DURATION="$(METRICS_DURATION)" METRICS_SAMPLES="$(METRICS_SAMPLES)" METRICS_OUT="$(METRICS_OUT)" METRICS_APPEND="$(METRICS_APPEND)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench metrics plan

.PHONY: metrics-plan-json
metrics-plan-json:
	METRICS_INTERVAL="$(METRICS_INTERVAL)" METRICS_DURATION="$(METRICS_DURATION)" METRICS_SAMPLES="$(METRICS_SAMPLES)" METRICS_OUT="$(METRICS_OUT)" METRICS_APPEND="$(METRICS_APPEND)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench metrics plan --json

.PHONY: diagnostics-list
diagnostics-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench diagnostics list

.PHONY: diagnostics-show
diagnostics-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench diagnostics show "$(DIAGNOSTIC)"

.PHONY: diagnostics-run
diagnostics-run: runtime-up
	./scripts/run_diagnostic.sh run "$(DIAGNOSTIC)"

.PHONY: workload-list
workload-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload list --raw

.PHONY: workload-list-go
workload-list-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload list

.PHONY: workload-show-go
workload-show-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload show "$(WORKLOAD_SPEC)"

.PHONY: workload-plan
workload-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan --raw "$(WORKLOAD_SPEC)"

.PHONY: workload-plan-go
workload-plan-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan "$(WORKLOAD_SPEC)"

.PHONY: workload-plan-json
workload-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan --json "$(WORKLOAD_SPEC)"

.PHONY: dataset-list
dataset-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset list --raw

.PHONY: dataset-list-go
dataset-list-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset list

.PHONY: dataset-show-go
dataset-show-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset show "$(DATASET_SPEC)"

.PHONY: dataset-plan
dataset-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan --raw "$(DATASET_SPEC)"

.PHONY: dataset-plan-go
dataset-plan-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan "$(DATASET_SPEC)"

.PHONY: dataset-plan-json
dataset-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan --json "$(DATASET_SPEC)"

.PHONY: dataset-show
dataset-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset show --raw "$(DATASET_SPEC)"

.PHONY: dataset-load
dataset-load: runtime-up
	DATASET_SIZE="$(DATASET_SIZE)" DATASET_SEED="$(DATASET_SEED)" DATASET_SCHEMA="$(DATASET_SCHEMA)" ./scripts/load_dataset.sh load "$(DATASET_SPEC)"

.PHONY: experiment-list
experiment-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment list --raw

.PHONY: experiment-show
experiment-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment show --raw "$(EXPERIMENT_SPEC)"

.PHONY: experiment-plan
experiment-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan "$(EXPERIMENT_SPEC)"

.PHONY: experiment-plan-json
experiment-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --json "$(EXPERIMENT_SPEC)"

.PHONY: experiment-plan-expanded
experiment-plan-expanded:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --expanded "$(EXPERIMENT_SPEC)"

.PHONY: experiment-plan-expanded-json
experiment-plan-expanded-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --json --expanded "$(EXPERIMENT_SPEC)"

.PHONY: experiment-run
experiment-run:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) experiment run --runtime "$(PGWORKBENCH_RUNTIME)" "$(EXPERIMENT_SPEC)"

.PHONY: run-list
run-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run list $(RUN_LIST_ARGS)

.PHONY: run-list-json
run-list-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run list --json $(RUN_LIST_ARGS)

.PHONY: run-show
run-show:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make run-show RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run show "$(RUN_DIR)"

.PHONY: run-show-json
run-show-json:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make run-show-json RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run show --json "$(RUN_DIR)"

.PHONY: run-bundle
run-bundle:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make run-bundle RUN_DIR=runs/<run-id> [RUN_BUNDLE_OUT=generated/run.tar.gz]' >&2; exit 2; }
	@if [[ -n "$(RUN_BUNDLE_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run bundle "$(RUN_DIR)" "$(RUN_BUNDLE_OUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run bundle "$(RUN_DIR)"; \
	fi

.PHONY: run-bundle-json
run-bundle-json:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make run-bundle-json RUN_DIR=runs/<run-id> [RUN_BUNDLE_OUT=generated/run.tar.gz]' >&2; exit 2; }
	@if [[ -n "$(RUN_BUNDLE_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run bundle --json "$(RUN_DIR)" "$(RUN_BUNDLE_OUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run bundle --json "$(RUN_DIR)"; \
	fi

.PHONY: experiment-report
experiment-report:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report run "$(RUN_DIR)"

.PHONY: experiment-report-shell
experiment-report-shell:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report-shell RUN_DIR=runs/<run-id>' >&2; exit 2; }
	./scripts/report_run.sh "$(RUN_DIR)"

.PHONY: experiment-verify
experiment-verify:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-verify RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify "$(RUN_DIR)"

.PHONY: experiment-verify-json
experiment-verify-json:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-verify-json RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify --json "$(RUN_DIR)"

.PHONY: experiment-verify-bundle
experiment-verify-bundle:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-verify-bundle RUN_DIR=<extracted-run-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify --bundle "$(RUN_DIR)"

.PHONY: experiment-verify-bundle-json
experiment-verify-bundle-json:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-verify-bundle-json RUN_DIR=<extracted-run-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run verify --json --bundle "$(RUN_DIR)"

.PHONY: experiment-report-go
experiment-report-go:
	@test -n "$(RUN_DIR)" || { echo 'Usage: make experiment-report-go RUN_DIR=runs/<run-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report run "$(RUN_DIR)"

.PHONY: experiment-summary
experiment-summary:
	@test -n "$(SUMMARY_INPUT)" || { echo 'Usage: make experiment-summary SUMMARY_INPUT=runs/repeats/<id>' >&2; exit 2; }
	@if [[ -n "$(SUMMARY_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report summary --output "$(SUMMARY_OUT)" "$(SUMMARY_INPUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report summary "$(SUMMARY_INPUT)"; \
	fi

.PHONY: experiment-summary-shell
experiment-summary-shell:
	@test -n "$(SUMMARY_INPUT)" || { echo 'Usage: make experiment-summary-shell SUMMARY_INPUT=runs/repeats/<id>' >&2; exit 2; }
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
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report history --output "$(HISTORY_OUT)" $(HISTORY_INPUTS); \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report history $(HISTORY_INPUTS); \
	fi

.PHONY: experiment-history-shell
experiment-history-shell:
	@test -n "$(HISTORY_INPUTS)" || { echo 'Usage: make experiment-history-shell HISTORY_INPUTS="runs/repeats/a runs/repeats/b"' >&2; exit 2; }
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
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report compare --raw "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

.PHONY: experiment-compare-shell
experiment-compare-shell:
	@test -n "$(BASELINE_RUN)" || { echo 'Usage: make experiment-compare-shell BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	@test -n "$(CANDIDATE_RUN)" || { echo 'Usage: make experiment-compare-shell BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	./scripts/compare_runs.sh "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

.PHONY: experiment-compare-go
experiment-compare-go:
	@test -n "$(BASELINE_RUN)" || { echo 'Usage: make experiment-compare-go BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	@test -n "$(CANDIDATE_RUN)" || { echo 'Usage: make experiment-compare-go BASELINE_RUN=runs/a CANDIDATE_RUN=runs/b' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench report compare "$(BASELINE_RUN)" "$(CANDIDATE_RUN)"

.PHONY: benchmark-list
benchmark-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark list --raw

.PHONY: benchmark-show
benchmark-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark show --raw "$(BENCHMARK_SPEC)"

.PHONY: benchmark-validate
benchmark-validate:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark validate

.PHONY: benchmark-plan
benchmark-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark plan "$(BENCHMARK_SPEC)"

.PHONY: benchmark-plan-json
benchmark-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark plan --json "$(BENCHMARK_SPEC)"

.PHONY: benchmark-drivers
benchmark-drivers:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark drivers

.PHONY: benchmark-driver-show
benchmark-driver-show:
	@test -n "$(BENCHMARK_DRIVER_ID)" || { echo 'Usage: make benchmark-driver-show BENCHMARK_DRIVER_ID=<pinned-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark driver-show "$(BENCHMARK_DRIVER_ID)"

.PHONY: benchmark-driver-run
benchmark-driver-run:
	@test "$(BENCHMARK_DRIVER_ACKNOWLEDGE)" = 1 || { echo 'Set BENCHMARK_DRIVER_ACKNOWLEDGE=1 only after confirming the loopback non-system target is disposable and non-production.' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_ID)" || { echo 'BENCHMARK_DRIVER_ID is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_RUNTIME_ROOT)" || { echo 'BENCHMARK_DRIVER_RUNTIME_ROOT is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_BINARY)" || { echo 'BENCHMARK_DRIVER_BINARY is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_CONFIG)" || { echo 'BENCHMARK_DRIVER_CONFIG is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_SCRIPT)" || { echo 'BENCHMARK_DRIVER_SCRIPT is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_WORKLOAD)" || { echo 'BENCHMARK_DRIVER_WORKLOAD is required' >&2; exit 2; }
	@test -n "$(BENCHMARK_DRIVER_OUTPUT)" || { echo 'BENCHMARK_DRIVER_OUTPUT is required' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark driver-run \
		--acknowledge-external-disposable-target \
		--driver "$(BENCHMARK_DRIVER_ID)" --runtime-root "$(BENCHMARK_DRIVER_RUNTIME_ROOT)" \
		--binary "$(BENCHMARK_DRIVER_BINARY)" \
		--config "$(BENCHMARK_DRIVER_CONFIG)" --script "$(BENCHMARK_DRIVER_SCRIPT)" \
		--workload "$(BENCHMARK_DRIVER_WORKLOAD)" --timeout "$(BENCHMARK_DRIVER_TIMEOUT)" \
		"$(BENCHMARK_DRIVER_OUTPUT)"

.PHONY: benchmark-driver-verify
benchmark-driver-verify:
	@test -n "$(BENCHMARK_DRIVER_EXECUTION)" || { echo 'Usage: make benchmark-driver-verify BENCHMARK_DRIVER_EXECUTION=<artifact-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark driver-run-verify "$(BENCHMARK_DRIVER_EXECUTION)"

.PHONY: benchmark-host-inspect
benchmark-host-inspect:
	@test -n "$(BENCHMARK_HOST_OPTIONS)" || { echo 'BENCHMARK_HOST_OPTIONS must supply storage-path, storage-label, client-placement, and the intended strict policy; see docs/benchmark-host-qualification.md' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark host-inspect --output "$(BENCHMARK_HOST_OUTPUT)" $(BENCHMARK_HOST_OPTIONS)

.PHONY: benchmark-host-verify
benchmark-host-verify:
	@test -n "$(BENCHMARK_HOST_INPUT)" || { echo 'Usage: make benchmark-host-verify BENCHMARK_HOST_INPUT=<host-qualification.json>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark host-verify "$(BENCHMARK_HOST_INPUT)"

.PHONY: operation-list
operation-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation list

.PHONY: operation-show
operation-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation show "$(OPERATION_BENCHMARK_SPEC)"

.PHONY: operation-run
operation-run:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation run --runtime "$(PGWORKBENCH_RUNTIME)" $(OPERATION_BENCHMARK_RUN_ID_ARG) "$(OPERATION_BENCHMARK_SPEC)"

.PHONY: operation-run-show
operation-run-show:
	@test -n "$(OPERATION_BENCHMARK_SERIES)" || { echo 'Usage: make operation-run-show OPERATION_BENCHMARK_SERIES=<series-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation run-show "$(OPERATION_BENCHMARK_SERIES)"

.PHONY: operation-verify
operation-verify:
	@test -n "$(OPERATION_BENCHMARK_SERIES)" || { echo 'Usage: make operation-verify OPERATION_BENCHMARK_SERIES=<series-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation verify "$(OPERATION_BENCHMARK_SERIES)"

.PHONY: operation-verify-bundle
operation-verify-bundle:
	@test -n "$(OPERATION_BENCHMARK_SERIES)" || { echo 'Usage: make operation-verify-bundle OPERATION_BENCHMARK_SERIES=<extracted-series-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation verify --bundle "$(OPERATION_BENCHMARK_SERIES)"

.PHONY: operation-bundle
operation-bundle:
	@test -n "$(OPERATION_BENCHMARK_SERIES)" || { echo 'Usage: make operation-bundle OPERATION_BENCHMARK_SERIES=<series-id-or-dir> [OPERATION_BENCHMARK_BUNDLE_OUT=generated/operation-benchmark.tar.gz]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark operation bundle "$(OPERATION_BENCHMARK_SERIES)" $(if $(strip $(OPERATION_BENCHMARK_BUNDLE_OUT)),"$(OPERATION_BENCHMARK_BUNDLE_OUT)",)

.PHONY: benchmark-run
benchmark-run:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run --runtime "$(PGWORKBENCH_RUNTIME)" $(BENCHMARK_RUN_ID_ARG) --subject "$(BENCHMARK_SUBJECT)" "$(BENCHMARK_SPEC)"

.PHONY: benchmark-run-json
benchmark-run-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run --json --runtime "$(PGWORKBENCH_RUNTIME)" $(BENCHMARK_RUN_ID_ARG) --subject "$(BENCHMARK_SUBJECT)" "$(BENCHMARK_SPEC)"

.PHONY: benchmark-run-show
benchmark-run-show:
	@test -n "$(BENCHMARK_SERIES)" || { echo 'Usage: make benchmark-run-show BENCHMARK_SERIES=<series-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run-show "$(BENCHMARK_SERIES)"

.PHONY: benchmark-run-verify
benchmark-run-verify:
	@test -n "$(BENCHMARK_SERIES)" || { echo 'Usage: make benchmark-run-verify BENCHMARK_SERIES=<series-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run-verify "$(BENCHMARK_SERIES)"

.PHONY: benchmark-run-verify-bundle
benchmark-run-verify-bundle:
	@test -n "$(BENCHMARK_SERIES)" || { echo 'Usage: make benchmark-run-verify-bundle BENCHMARK_SERIES=<extracted-series-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run-verify --bundle "$(BENCHMARK_SERIES)"

.PHONY: benchmark-run-bundle
benchmark-run-bundle:
	@test -n "$(BENCHMARK_SERIES)" || { echo 'Usage: make benchmark-run-bundle BENCHMARK_SERIES=<series-id-or-dir> [BENCHMARK_BUNDLE_OUT=generated/benchmark.tar.gz]' >&2; exit 2; }
	@if [[ -n "$(BENCHMARK_BUNDLE_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run-bundle "$(BENCHMARK_SERIES)" "$(BENCHMARK_BUNDLE_OUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark run-bundle "$(BENCHMARK_SERIES)"; \
	fi

.PHONY: benchmark-compare
benchmark-compare:
	@test -n "$(BENCHMARK_BASELINE)" || { echo 'Usage: make benchmark-compare BENCHMARK_BASELINE=<series-a> BENCHMARK_CANDIDATE=<series-b>' >&2; exit 2; }
	@test -n "$(BENCHMARK_CANDIDATE)" || { echo 'Usage: make benchmark-compare BENCHMARK_BASELINE=<series-a> BENCHMARK_CANDIDATE=<series-b>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark compare "$(BENCHMARK_BASELINE)" "$(BENCHMARK_CANDIDATE)"

.PHONY: benchmark-history-create
benchmark-history-create:
	@test -n "$(BENCHMARK_HISTORY_INPUTS)" || { echo 'Usage: make benchmark-history-create BENCHMARK_HISTORY_INPUTS="<series-a> <series-b> [...]" [BENCHMARK_HISTORY_ID=id]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark history-create $(if $(strip $(BENCHMARK_HISTORY_ID)),--history-id "$(BENCHMARK_HISTORY_ID)",) $(BENCHMARK_HISTORY_INPUTS)

.PHONY: benchmark-history-show
benchmark-history-show:
	@test -n "$(BENCHMARK_HISTORY)" || { echo 'Usage: make benchmark-history-show BENCHMARK_HISTORY=<history-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark history-show "$(BENCHMARK_HISTORY)"

.PHONY: benchmark-history-verify
benchmark-history-verify:
	@test -n "$(BENCHMARK_HISTORY)" || { echo 'Usage: make benchmark-history-verify BENCHMARK_HISTORY=<history-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark history-verify "$(BENCHMARK_HISTORY)"

.PHONY: benchmark-history-verify-bundle
benchmark-history-verify-bundle:
	@test -n "$(BENCHMARK_HISTORY)" || { echo 'Usage: make benchmark-history-verify-bundle BENCHMARK_HISTORY=<extracted-history-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark history-verify --bundle "$(BENCHMARK_HISTORY)"

.PHONY: benchmark-history-bundle
benchmark-history-bundle:
	@test -n "$(BENCHMARK_HISTORY)" || { echo 'Usage: make benchmark-history-bundle BENCHMARK_HISTORY=<history-id-or-dir> [BENCHMARK_HISTORY_BUNDLE_OUT=generated/history.tar.gz]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark history-bundle "$(BENCHMARK_HISTORY)" $(if $(strip $(BENCHMARK_HISTORY_BUNDLE_OUT)),"$(BENCHMARK_HISTORY_BUNDLE_OUT)",)

.PHONY: benchmark-import-verify
benchmark-import-verify:
	@test -n "$(BENCHMARK_IMPORT)" || { echo 'Usage: make benchmark-import-verify BENCHMARK_IMPORT=<import-dir-or-result.json>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark import-verify "$(BENCHMARK_IMPORT)"

.PHONY: benchmark-import-verify-bundle
benchmark-import-verify-bundle:
	@test -n "$(BENCHMARK_IMPORT)" || { echo 'Usage: make benchmark-import-verify-bundle BENCHMARK_IMPORT=<extracted-import-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark import-verify --bundle "$(BENCHMARK_IMPORT)"

.PHONY: benchmark-import-bundle
benchmark-import-bundle:
	@test -n "$(BENCHMARK_IMPORT)" || { echo 'Usage: make benchmark-import-bundle BENCHMARK_IMPORT=<import-dir-or-result.json> [BENCHMARK_IMPORT_BUNDLE_OUT=generated/import.tar.gz]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark import-bundle "$(BENCHMARK_IMPORT)" $(if $(strip $(BENCHMARK_IMPORT_BUNDLE_OUT)),"$(BENCHMARK_IMPORT_BUNDLE_OUT)",)

.PHONY: benchmark-campaign-run
benchmark-campaign-run:
	@test -n "$(BENCHMARK_CAMPAIGN_INPUTS)" || { echo 'Usage: make benchmark-campaign-run BENCHMARK_CAMPAIGN_INPUTS="<benchmark> [...]" [BENCHMARK_CAMPAIGN_ID=id]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark campaign-run --runtime "$(PGWORKBENCH_RUNTIME)" $(if $(strip $(BENCHMARK_CAMPAIGN_ID)),--campaign-id "$(BENCHMARK_CAMPAIGN_ID)",) --subject "$(BENCHMARK_CAMPAIGN_SUBJECT)" $(BENCHMARK_CAMPAIGN_INPUTS)

.PHONY: benchmark-campaign-show
benchmark-campaign-show:
	@test -n "$(BENCHMARK_CAMPAIGN)" || { echo 'Usage: make benchmark-campaign-show BENCHMARK_CAMPAIGN=<campaign-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark campaign-show "$(BENCHMARK_CAMPAIGN)"

.PHONY: benchmark-campaign-verify
benchmark-campaign-verify:
	@test -n "$(BENCHMARK_CAMPAIGN)" || { echo 'Usage: make benchmark-campaign-verify BENCHMARK_CAMPAIGN=<campaign-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark campaign-verify "$(BENCHMARK_CAMPAIGN)"

.PHONY: benchmark-campaign-verify-bundle
benchmark-campaign-verify-bundle:
	@test -n "$(BENCHMARK_CAMPAIGN)" || { echo 'Usage: make benchmark-campaign-verify-bundle BENCHMARK_CAMPAIGN=<extracted-campaign-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark campaign-verify --bundle "$(BENCHMARK_CAMPAIGN)"

.PHONY: benchmark-campaign-bundle
benchmark-campaign-bundle:
	@test -n "$(BENCHMARK_CAMPAIGN)" || { echo 'Usage: make benchmark-campaign-bundle BENCHMARK_CAMPAIGN=<campaign-id-or-dir> [BENCHMARK_CAMPAIGN_BUNDLE_OUT=generated/campaign.tar.gz]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark campaign-bundle "$(BENCHMARK_CAMPAIGN)" $(if $(strip $(BENCHMARK_CAMPAIGN_BUNDLE_OUT)),"$(BENCHMARK_CAMPAIGN_BUNDLE_OUT)",)

.PHONY: benchmark-ab-run
benchmark-ab-run:
	@test -n "$(BENCHMARK_AB_BASELINE)" || { echo 'Usage: make benchmark-ab-run BENCHMARK_AB_BASELINE=<spec-a> BENCHMARK_AB_CANDIDATE=<spec-b> BENCHMARK_AB_OPTIONS="<complete qualification options>"' >&2; exit 2; }
	@test -n "$(BENCHMARK_AB_CANDIDATE)" || { echo 'Usage: make benchmark-ab-run BENCHMARK_AB_BASELINE=<spec-a> BENCHMARK_AB_CANDIDATE=<spec-b> BENCHMARK_AB_OPTIONS="<complete qualification options>"' >&2; exit 2; }
	@test -n "$(BENCHMARK_AB_OPTIONS)" || { echo 'BENCHMARK_AB_OPTIONS must supply the complete explicit host-qualification policy; see docs/benchmark-ab.md' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark ab-run --runtime "$(PGWORKBENCH_RUNTIME)" $(if $(strip $(BENCHMARK_AB_RUN_ID)),--run-id "$(BENCHMARK_AB_RUN_ID)",) $(BENCHMARK_AB_OPTIONS) "$(BENCHMARK_AB_BASELINE)" "$(BENCHMARK_AB_CANDIDATE)"

.PHONY: benchmark-ab-show
benchmark-ab-show:
	@test -n "$(BENCHMARK_AB_RUN)" || { echo 'Usage: make benchmark-ab-show BENCHMARK_AB_RUN=<ab-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark ab-show "$(BENCHMARK_AB_RUN)"

.PHONY: benchmark-ab-verify
benchmark-ab-verify:
	@test -n "$(BENCHMARK_AB_RUN)" || { echo 'Usage: make benchmark-ab-verify BENCHMARK_AB_RUN=<ab-id-or-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark ab-verify "$(BENCHMARK_AB_RUN)"

.PHONY: benchmark-ab-verify-bundle
benchmark-ab-verify-bundle:
	@test -n "$(BENCHMARK_AB_RUN)" || { echo 'Usage: make benchmark-ab-verify-bundle BENCHMARK_AB_RUN=<extracted-ab-dir>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark ab-verify --bundle "$(BENCHMARK_AB_RUN)"

.PHONY: benchmark-ab-bundle
benchmark-ab-bundle:
	@test -n "$(BENCHMARK_AB_RUN)" || { echo 'Usage: make benchmark-ab-bundle BENCHMARK_AB_RUN=<ab-id-or-dir> [BENCHMARK_AB_BUNDLE_OUT=generated/ab.tar.gz]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) benchmark ab-bundle "$(BENCHMARK_AB_RUN)" $(if $(strip $(BENCHMARK_AB_BUNDLE_OUT)),"$(BENCHMARK_AB_BUNDLE_OUT)",)

.PHONY: pgdrill-baseline-export
pgdrill-baseline-export:
	@test -n "$(PGDRILL_SOURCE)" || { echo 'Usage: make pgdrill-baseline-export PGDRILL_SOURCE=<run-or-bundle> PGDRILL_BASELINE=<output.json> [PGDRILL_PREDICATE_FILE=file]' >&2; exit 2; }
	@test -n "$(PGDRILL_BASELINE)" || { echo 'Usage: make pgdrill-baseline-export PGDRILL_SOURCE=<run-or-bundle> PGDRILL_BASELINE=<output.json> [PGDRILL_PREDICATE_FILE=file]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) bridge pgdrill export $(if $(filter 1,$(PGDRILL_REQUIRE_BUNDLE)),--bundle,) $(if $(strip $(PGDRILL_PREDICATE_FILE)),--reviewed-predicate-file "$(PGDRILL_PREDICATE_FILE)",) "$(PGDRILL_SOURCE)" "$(PGDRILL_BASELINE)"

.PHONY: pgdrill-baseline-verify
pgdrill-baseline-verify:
	@test -n "$(PGDRILL_BASELINE)" || { echo 'Usage: make pgdrill-baseline-verify PGDRILL_BASELINE=<baseline.json> [PGDRILL_SOURCE=run-or-bundle]' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) bridge pgdrill verify $(if $(strip $(PGDRILL_SOURCE)),--source "$(PGDRILL_SOURCE)",) "$(PGDRILL_BASELINE)"

.PHONY: matrix-list
matrix-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix list --raw

.PHONY: matrix-show
matrix-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix show --raw "$(MATRIX_SPEC)"

.PHONY: matrix-plan
matrix-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix plan --raw "$(MATRIX_SPEC)"

.PHONY: matrix-plan-go
matrix-plan-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix plan "$(MATRIX_SPEC)"

.PHONY: matrix-plan-json
matrix-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix plan --json "$(MATRIX_SPEC)"

.PHONY: matrix-run
matrix-run:
	./scripts/run_experiment_matrix.sh run "$(MATRIX_SPEC)"

.PHONY: matrix-candidate-verify
matrix-candidate-verify:
	@test -n "$(MATRIX_RUN)" || { echo 'Usage: make matrix-candidate-verify MATRIX_RUN=runs/matrices/<id> MATRIX_EXPECTED_RUNS=<count> VERSION=<version> BUILD_COMMIT=<full-commit> PGWORKBENCH_CLI=<candidate-binary>' >&2; exit 2; }
	@test -n "$(MATRIX_EXPECTED_RUNS)" || { echo 'MATRIX_EXPECTED_RUNS is required' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) matrix verify-candidate \
		--version "$(VERSION)" --commit "$(BUILD_COMMIT)" --expected-runs "$(MATRIX_EXPECTED_RUNS)" "$(MATRIX_RUN)"

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
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload show --raw "$(WORKLOAD_SPEC)"

.PHONY: workload-run
workload-run:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload run "$(WORKLOAD_SPEC)"

.PHONY: workload-run-json
workload-run-json:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload run --json "$(WORKLOAD_SPEC)"

.PHONY: workload-run-shell
workload-run-shell:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/run_workload.sh run "$(WORKLOAD_SPEC)"

.PHONY: utility-list
utility-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility list --raw

.PHONY: utility-show
utility-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility show --raw "$(UTILITY_TEST_SPEC)"

.PHONY: utility-validate
utility-validate:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility validate

.PHONY: utility-plan
utility-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan "$(UTILITY_TEST_SPEC)"

.PHONY: utility-plan-json
utility-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan --json "$(UTILITY_TEST_SPEC)"

.PHONY: utility-plan-expanded
utility-plan-expanded:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan --expanded "$(UTILITY_TEST_SPEC)"

.PHONY: utility-run
utility-run:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) utility run --runtime "$(PGWORKBENCH_RUNTIME)" $(UTILITY_RUN_ID_ARG) "$(UTILITY_TEST_SPEC)"

.PHONY: utility-run-json
utility-run-json:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) utility run --json --runtime "$(PGWORKBENCH_RUNTIME)" $(UTILITY_RUN_ID_ARG) "$(UTILITY_TEST_SPEC)"

.PHONY: utility-suite-list
utility-suite-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite list --raw

.PHONY: utility-suite-show
utility-suite-show:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite show --raw "$(UTILITY_SUITE)"

.PHONY: utility-suite-validate
utility-suite-validate:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite validate

.PHONY: utility-suite-plan
utility-suite-plan:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite plan "$(UTILITY_SUITE)"

.PHONY: utility-suite-plan-json
utility-suite-plan-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite plan --json "$(UTILITY_SUITE)"

.PHONY: utility-suite-run
utility-suite-run:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) utility-suite run "$(UTILITY_SUITE)"

.PHONY: utility-suite-run-json
utility-suite-run-json:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) utility-suite run --json "$(UTILITY_SUITE)"

.PHONY: utility-suite-run-list
utility-suite-run-list:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-list $(UTILITY_SUITE_RUN_INPUTS)

.PHONY: utility-suite-run-list-json
utility-suite-run-list-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-list --json $(UTILITY_SUITE_RUN_INPUTS)

.PHONY: utility-suite-run-show
utility-suite-run-show:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-show UTILITY_SUITE_RUN=<suite-run-dir-or-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-show "$(UTILITY_SUITE_RUN)"

.PHONY: utility-suite-run-show-json
utility-suite-run-show-json:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-show-json UTILITY_SUITE_RUN=<suite-run-dir-or-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-show --json "$(UTILITY_SUITE_RUN)"

.PHONY: utility-suite-run-bundle
utility-suite-run-bundle:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-bundle UTILITY_SUITE_RUN=<suite-run-dir-or-id> [UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz]' >&2; exit 2; }
	@if [[ -n "$(UTILITY_SUITE_BUNDLE_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-bundle "$(UTILITY_SUITE_RUN)" "$(UTILITY_SUITE_BUNDLE_OUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-bundle "$(UTILITY_SUITE_RUN)"; \
	fi

.PHONY: utility-suite-run-bundle-json
utility-suite-run-bundle-json:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-bundle-json UTILITY_SUITE_RUN=<suite-run-dir-or-id> [UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz]' >&2; exit 2; }
	@if [[ -n "$(UTILITY_SUITE_BUNDLE_OUT)" ]]; then \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-bundle --json "$(UTILITY_SUITE_RUN)" "$(UTILITY_SUITE_BUNDLE_OUT)"; \
	else \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-bundle --json "$(UTILITY_SUITE_RUN)"; \
	fi

.PHONY: utility-suite-run-verify
utility-suite-run-verify:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-verify UTILITY_SUITE_RUN=<suite-run-dir-or-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-verify "$(UTILITY_SUITE_RUN)"

.PHONY: utility-suite-run-verify-json
utility-suite-run-verify-json:
	@test -n "$(UTILITY_SUITE_RUN)" || { echo 'Usage: make utility-suite-run-verify-json UTILITY_SUITE_RUN=<suite-run-dir-or-id>' >&2; exit 2; }
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-verify --json "$(UTILITY_SUITE_RUN)"

.PHONY: scan-artifacts
scan-artifacts:
	./scripts/scan_pg_failures.sh $(SCAN_PATHS)

.PHONY: scan-artifacts-go
scan-artifacts-go:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench scan failures $(SCAN_PATHS)

.PHONY: privacy-scan
privacy-scan:
	./scripts/privacy_scan.sh

.PHONY: workload-start
workload-start: runtime-up
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/workload_bg.sh start-profile "$(PROFILE)" "$(WORKLOAD_SQL)"

.PHONY: workload-start-spec
workload-start-spec:
	PROFILE_SIZE="$(PROFILE_SIZE)" PROFILE_SECONDS="$(PROFILE_SECONDS)" ./scripts/workload_bg.sh start-spec "$(WORKLOAD_SPEC)"

.PHONY: workload-start-sql
workload-start-sql: runtime-up
	@test -n "$(SQL)" || { echo 'Usage: make workload-start-sql SQL=path/to/file.sql' >&2; exit 2; }
	./scripts/workload_bg.sh start-sql "$(SQL)"

.PHONY: workload-start-noisia
workload-start-noisia: docker-up
	NOISIA_DURATION="$(NOISIA_DURATION)" NOISIA_JOBS="$(NOISIA_JOBS)" ./scripts/workload_bg.sh start-noisia "$(WORKLOAD)"

.PHONY: workload-status
workload-status:
	./scripts/workload_bg.sh status

.PHONY: workload-status-json
workload-status-json:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload bg status --json

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
run-sql: runtime-up
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

.PHONY: candidate-preflight
candidate-preflight:
	BUILD_COMMIT="$(BUILD_COMMIT)" PGWORKBENCH_GO="$(GO)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" ./scripts/candidate_preflight.sh "$(VERSION)"

.PHONY: release-snapshot
release-snapshot: candidate-preflight
	mkdir -p "$(RELEASE_DIR)"
	@set -euo pipefail; for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}"; \
		out_dir="$(RELEASE_DIR)/$${name}"; \
		archive="$(RELEASE_DIR)/$${name}.tar.gz"; \
		sbom="$(RELEASE_DIR)/$${name}.spdx.json"; \
		rm -rf "$$out_dir"; rm -f "$$archive" "$$sbom"; \
		echo "building $$name"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) run ./cmd/pgworkbench pack export --engine-version "$(VERSION)" "$$out_dir"; \
		CGO_ENABLED=0 GOOS="$$os" GOARCH="$$arch" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) build -trimpath -ldflags '$(PGWORKBENCH_LDFLAGS)' -o "$$out_dir/pgworkbench" ./cmd/pgworkbench; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) run ./cmd/pgworkbench release sbom create --root "$$out_dir" --output "$$sbom" \
				--name "$$name" --version "$(VERSION)" --commit "$(BUILD_COMMIT)" --epoch "$(SOURCE_DATE_EPOCH)"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) run ./cmd/pgworkbench release sbom verify --package-root "$$out_dir" "$$sbom"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(GO) run ./cmd/pgworkbench release archive create --source "$$out_dir" --output "$$archive" \
				--root-name "$$name" --epoch "$(SOURCE_DATE_EPOCH)"; \
		rm -rf "$$out_dir"; \
	done
	@rm -f "$(RELEASE_CHECKSUM_FILE)"
	@set -euo pipefail; for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}.tar.gz"; \
		if command -v sha256sum >/dev/null 2>&1; then \
			(cd "$(RELEASE_DIR)" && sha256sum "$$name") >> "$(RELEASE_CHECKSUM_FILE)"; \
		else \
			(cd "$(RELEASE_DIR)" && shasum -a 256 "$$name") >> "$(RELEASE_CHECKSUM_FILE)"; \
		fi; \
	done
	@LC_ALL=C sort -k2,2 -o "$(RELEASE_CHECKSUM_FILE)" "$(RELEASE_CHECKSUM_FILE)"
	@chmod 0644 "$(RELEASE_CHECKSUM_FILE)"
	@rm -f "$(RELEASE_MANIFEST_FILE)"
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench release manifest create \
		--release-dir "$(RELEASE_DIR)" --version "$(VERSION)" --commit "$(BUILD_COMMIT)" --pack-root . \
		--checksum-file "$$(basename "$(RELEASE_CHECKSUM_FILE)")" \
		--output "$$(basename "$(RELEASE_MANIFEST_FILE)")" --source-date-epoch "$(SOURCE_DATE_EPOCH)"
	@GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench release manifest verify \
		--release-dir "$(RELEASE_DIR)" --manifest "$$(basename "$(RELEASE_MANIFEST_FILE)")"
	@for target in $(RELEASE_PLATFORMS); do \
		os="$${target%/*}"; \
		arch="$${target#*/}"; \
		name="pgworkbench-$(VERSION)-$${os}-$${arch}"; \
		printf '%s\n' "$(RELEASE_DIR)/$${name}.tar.gz"; \
		printf '%s\n' "$(RELEASE_DIR)/$${name}.spdx.json"; \
	done
	@printf '%s\n' "$(RELEASE_CHECKSUM_FILE)"
	@printf '%s\n' "$(RELEASE_MANIFEST_FILE)"

.PHONY: release-smoke
release-smoke: release-snapshot
	@set -euo pipefail; \
		manifest="$$(basename "$(RELEASE_MANIFEST_FILE)")"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench release manifest verify \
			--release-dir "$(RELEASE_DIR)" --manifest "$$manifest"; \
		host_os="$$($(GO) env GOOS)"; host_arch="$$($(GO) env GOARCH)"; \
		archive="$(RELEASE_DIR)/pgworkbench-$(VERSION)-$${host_os}-$${host_arch}.tar.gz"; \
		./tests/release_archive.sh "$$archive"; \
		PGWORKBENCH_GO="$(GO)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" ./tests/release_reproducibility.sh \
			"$$archive" "$(VERSION)" "$(BUILD_COMMIT)" "$(SOURCE_DATE_EPOCH)" "$(BUILD_DATE)"; \
		PGWORKBENCH_GO="$(GO)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" ./tests/release_set_reproducibility.sh \
			"$(RELEASE_DIR)" "$(VERSION)" "$(BUILD_COMMIT)" "$(SOURCE_DATE_EPOCH)" "$(BUILD_DATE)"

.PHONY: schema-check
schema-check:
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) test -count=1 ./internal/schemavalidation -run '^TestRepositorySchemaGate$$'

.PHONY: check
check: schema-check
	bash -n scripts/*.sh tests/*.sh profiles/*/scripts/*.sh
	@if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then git diff --check; fi
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) test ./...
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) vet ./...
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench pack validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench compatibility validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile list >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile show smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile plan smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench profile plan --json smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench diagnostics list >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench diagnostics show activity >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench patchset validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench source plan pg-source/check >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology show --raw single >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench topology inspect single >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench metrics plan >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench metrics plan --json >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix plan --raw smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix plan --json smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment show --raw smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench matrix show --raw smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload show --raw pgbench/tiny >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan pgbench/tiny >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan --raw pgbench/tiny >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload plan --json pgbench/tiny >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark show --raw pgbench/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark plan pgbench/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark plan --json pgbench/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark drivers --json >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark driver-show sysbench-postgresql-1.0.20 >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark operation list --json >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench benchmark operation show maintenance/vacuum-bloat-manual >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" ./tests/benchmark_import.sh >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload bg status --json >/dev/null
	PG_UPGRADE_ACTION=plan WORKLOAD_RUN_LOG=0 GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench workload run topology/native-pg-upgrade >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility show --raw pg-dump/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan pg-dump/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan --json pg-dump/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility plan --expanded pg-dump/smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite show --raw native-dump >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite plan native-dump >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite plan --json native-dump >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-list >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench utility-suite run-list --json >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset list --raw >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset show --raw synthetic/items >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan synthetic/items >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan --raw synthetic/items >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench dataset plan --json synthetic/items >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec validate >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec reference all >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench spec schema all >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run list >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench run list --json >/dev/null
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" ./tests/spec_docs.sh
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --json smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --expanded smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench experiment plan --json --expanded smoke >/dev/null
	GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(GO) run ./cmd/pgworkbench scan failures $(SCAN_PATHS) >/dev/null
	./tests/profile_catalog.sh
	./tests/patchsets.sh
	./tests/diagnostics.sh
	./tests/shell_portability.sh
	./tests/sample_metrics_readiness.sh
	./tests/process_lifecycle.sh
	./tests/runtime.sh
	./tests/runtime_workloads.sh
	./tests/pgbench_phase_io.sh
	./tests/benchmark_capsule.sh
	./tests/compose_loopback_ports.sh
	./tests/candidate_identity.sh
	./tests/release_workflow_graph.sh
	./tests/external_driver_gate.sh
	./tests/benchmark_phase.sh
	./tests/effective_pg_settings.sh
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" bash ./tests/benchmark_preflight.sh
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" ./tests/experiment_terminal.sh
	GO_CACHE="$(GO_CACHE)" GO_MOD_CACHE="$(GO_MOD_CACHE)" ./tests/utility_provenance.sh
	bash ./tests/experiment_hooks.sh
	./tests/experiment_target_guard.sh
	./tests/experiment_runs_root.sh
	./tests/privacy_scan.sh
	./tests/scan_failures.sh
	./tests/run_verify.sh
	./tests/report_runs.sh
	./tests/summarize_runs.sh
	./tests/compare_runs.sh
	./tests/history.sh

.PHONY: native-test
native-test:
	./tests/runtime.sh
	./tests/runtime_workloads.sh
	@set -euo pipefail; bindir="$(PGWORKBENCH_NATIVE_BINDIR)"; \
		if [[ -z "$$bindir" ]] && command -v pg_config >/dev/null 2>&1; then bindir="$$(pg_config --bindir)"; fi; \
		if [[ -z "$$bindir" || ! -x "$$bindir/initdb" ]]; then \
			echo 'FAIL: native PostgreSQL server binaries not found; set PGWORKBENCH_NATIVE_BINDIR' >&2; exit 1; \
		fi; \
		ports="$$(./scripts/assign_test_ports.sh)"; \
		port="$$(awk -F= '$$1 == "POSTGRES_PORT" {print $$2}' <<< "$$ports")"; \
		run_stamp="$$(date -u +%Y%m%d_%H%M%S)"; \
		configured_id="native-configured-$$run_stamp"; \
		default_id="native-default-$$run_stamp"; \
		false_id="native-false-$$run_stamp"; \
		metrics_fail_id="native-metrics-fail-$$run_stamp"; \
		background_fail_id="native-background-fail-$$run_stamp"; \
		benchmark_id="native-benchmark-smoke-$$run_stamp"; \
		utility_dump_id="native-utility-pgdump-$$run_stamp"; \
		utility_dumpall_id="native-utility-pgdumpall-$$run_stamp"; \
		utility_restore_id="native-utility-pgrestore-$$run_stamp"; \
		bundle_root="$$(mktemp -d "$${TMPDIR:-/tmp}/pgworkbench-native-bundle.XXXXXX")"; \
		cleanup_native() { POSTGRES_PORT="$$port" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$$bindir" ./scripts/runtime.sh down single >/dev/null 2>&1 || true; rm -rf -- "$$bundle_root"; }; \
		trap cleanup_native EXIT; \
		POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" EXPERIMENT_RUNTIME_RESET=1 \
		EXPERIMENT_PG_CONFIG=debug-logging EXPERIMENT_METRICS=0 EXPERIMENT_SNAPSHOT=0 \
			GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) experiment run --runtime native --run-id "$$configured_id" smoke; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) run verify "runs/$$configured_id"; \
		test "$$(POSTGRES_PORT="$$port" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$$bindir" ./scripts/psql.sh -Atq -c 'SHOW log_min_duration_statement')" = '0'; \
		POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" EXPERIMENT_PG_CONFIG=default \
		EXPERIMENT_METRICS=0 EXPERIMENT_SNAPSHOT=0 GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) experiment run --runtime native --run-id "$$default_id" smoke; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) run verify "runs/$$default_id"; \
		test "$$(POSTGRES_PORT="$$port" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$$bindir" ./scripts/psql.sh -Atq -c 'SHOW log_min_duration_statement')" = '-1'; \
		for utility_spec in pg-dump/smoke pg-dumpall/smoke pg-restore/smoke; do \
			case "$$utility_spec" in \
				pg-dump/smoke) utility_id="$$utility_dump_id" ;; \
				pg-dumpall/smoke) utility_id="$$utility_dumpall_id" ;; \
				pg-restore/smoke) utility_id="$$utility_restore_id" ;; \
			esac; \
			POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" \
			UTILITY_TEST_SNAPSHOT=0 METRICS_SAMPLES=1 \
			GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
				$(PGWORKBENCH_CLI) utility run --runtime native --run-id "$$utility_id" "$$utility_spec"; \
			GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
				$(PGWORKBENCH_CLI) run verify "runs/$$utility_id"; \
			done; \
		for utility_id in "$$utility_dump_id" "$$utility_dumpall_id" "$$utility_restore_id"; do \
			test -s "runs/$$utility_id/artifacts/provenance/experiment-spec.env"; \
			test -s "runs/$$utility_id/artifacts/provenance/source-utility-test.env"; \
		done; \
		for captured in \
			"$$utility_dump_id/pg-dump-smoke.sql" \
			"$$utility_dumpall_id/pg-dumpall.sql" \
			"$$utility_restore_id/pg-restore-smoke.dump" \
			"$$utility_restore_id/pg-restore-smoke.dump.sql"; do \
			utility_id="$${captured%%/*}"; output="$${captured#*/}"; \
			test -s "runs/$$utility_id/artifacts/utility/logs/utility/$$output"; \
		done; \
		bundle_archive="$$bundle_root/pgrestore.tar.gz"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) run bundle "runs/$$utility_restore_id" "$$bundle_archive" >/dev/null; \
		mkdir -p "$$bundle_root/extracted"; tar -C "$$bundle_root/extracted" -xzf "$$bundle_archive"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) run verify --bundle "$$bundle_root/extracted/$$utility_restore_id"; \
		test "$$(POSTGRES_PORT="$$port" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$$bindir" ./scripts/psql.sh -Atq -c 'SELECT count(*) FROM restore_check.items')" = '10000'; \
		for utility_output in pg-dump-smoke.sql pg-dumpall.sql pg-restore-smoke.dump pg-restore-smoke.dump.sql; do test -s "logs/utility/$$utility_output"; done; \
		POSTGRES_PORT="$$port" PGWORKBENCH_RUNTIME=native PGWORKBENCH_NATIVE_BINDIR="$$bindir" \
			PGWORKBENCH_GO="$(GO)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			./tests/benchmark_smoke.sh "$$benchmark_id"; \
		if POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" EXPERIMENT_PG_CONFIG=default \
		EXPERIMENT_METRICS=0 EXPERIMENT_SNAPSHOT=0 EXPERIMENT_ASSERT_SQL= EXPERIMENT_ASSERT_TRUE_SQL='SELECT false' \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) experiment run --runtime native --run-id "$$false_id" smoke; then \
			echo 'FAIL: strict boolean SQL assertion accepted false' >&2; exit 1; \
		fi; \
		grep -q '"status": "failed"' "runs/$$false_id/verdict.json"; \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" $(PGWORKBENCH_CLI) run verify "runs/$$false_id"; \
		if POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" EXPERIMENT_PG_CONFIG=default \
		EXPERIMENT_METRICS_INTERVAL=invalid EXPERIMENT_METRICS_SAMPLES=1 EXPERIMENT_SNAPSHOT=0 \
		GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) experiment run --runtime native --run-id "$$metrics_fail_id" smoke; then \
			echo 'FAIL: invalid metrics configuration unexpectedly started an experiment' >&2; exit 1; \
		fi; \
		test ! -e "runs/$$metrics_fail_id"; \
		if POSTGRES_PORT="$$port" PGWORKBENCH_NATIVE_BINDIR="$$bindir" EXPERIMENT_PG_CONFIG=default \
		EXPERIMENT_BACKGROUND_SPECS=missing/background EXPERIMENT_BACKGROUND_WAIT=1 \
		EXPERIMENT_METRICS=0 EXPERIMENT_SNAPSHOT=0 GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" \
			$(PGWORKBENCH_CLI) experiment run --runtime native --run-id "$$background_fail_id" smoke; then \
			echo 'FAIL: background child failure produced a passing experiment' >&2; exit 1; \
		fi; \
		grep -q '"status": "failed"' "runs/$$background_fail_id/verdict.json"; \
		grep -Eq '^workload_exit="?[1-9][0-9]*"?$$' "runs/$$background_fail_id/verdict.env"; \
		cleanup_native; trap - EXIT; \
		echo 'PASS: native runtime, utility adapters, portable bundle, child-exit handling, config isolation, strict assertion, and evidence contract'

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
	./tests/massive_dml.sh
	./tests/report_runs.sh
	./tests/summarize_runs.sh
	./tests/compare_runs.sh
	./tests/history.sh
	./tests/matrices.sh
	PGWORKBENCH_RUNTIME=docker PGWORKBENCH_GO="$(GO)" GOCACHE="$(GO_CACHE)" GOMODCACHE="$(GO_MOD_CACHE)" ./tests/benchmark_smoke.sh

.PHONY: release-check
release-check: candidate-preflight doctor check native-test quickstart test scan-artifacts scan-artifacts-go pgworkbench privacy-scan release-smoke
	@echo 'PASS: release-check'
