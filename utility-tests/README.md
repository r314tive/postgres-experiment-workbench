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
UTILITY_TEST_TRUSTED_SHELL
UTILITY_TEST_EXPECT_FILES
UTILITY_TEST_ASSERT_SQL_FILES
UTILITY_TEST_ASSERT_SQL
UTILITY_TEST_ASSERT_SHELL
UTILITY_TEST_SCAN_PATHS
```

SQL assertions are declarative and need no trust marker. Both
`UTILITY_TEST_ASSERT_SHELL` and `UTILITY_TEST_EXPECT_FILES` execute host-shell
checks through the experiment runner, so specs using either field must set
`UTILITY_TEST_TRUSTED_SHELL=1`. The marker is explicit intent, not a sandbox;
utility specs and their packs must still be trusted and reviewed.
