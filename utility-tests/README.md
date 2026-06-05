# Utility Tests

Utility tests are reusable PostgreSQL tool scenarios. They describe the state
to prepare, optional background pressure, metrics sampling, and the foreground
workload that invokes a utility or external tool.

Run:

```bash
make utility-list
make utility-show UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-json UTILITY_TEST_SPEC=pg-dump/smoke
make utility-plan-expanded UTILITY_TEST_SPEC=pg-dump/smoke
make utility-run UTILITY_TEST_SPEC=pg-dump/smoke
make utility-run-json UTILITY_TEST_SPEC=pg-dump/smoke
```

Specs live under `utility-tests/**/*.env`. They are dry-run planning contracts;
the foreground utility action is still a normal workload spec under
`workloads/`. The run command translates the utility-test spec into an ignored
temporary experiment spec and writes normal experiment artifacts under `runs/`.
Batch scenarios live under `utility-suites/`.

Useful result-contract fields:

```text
UTILITY_TEST_EXPECT_FILES
UTILITY_TEST_ASSERT_SQL_FILES
UTILITY_TEST_ASSERT_SQL
UTILITY_TEST_ASSERT_SHELL
UTILITY_TEST_SCAN_PATHS
```
