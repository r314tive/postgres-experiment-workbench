# Utility Suites

Utility suites batch `utility-tests/**/*.env` scenarios across profile sizes
and repeats.

Run:

```bash
make utility-suite-list
make utility-suite-show UTILITY_SUITE=native-dump
make utility-suite-plan UTILITY_SUITE=native-dump
make utility-suite-plan-json UTILITY_SUITE=native-dump
make utility-suite-run UTILITY_SUITE=native-dump
make utility-suite-run-json UTILITY_SUITE=native-dump
make utility-suite-run-list
make utility-suite-run-show UTILITY_SUITE_RUN=<suite-run-id>
make utility-suite-run-bundle UTILITY_SUITE_RUN=<suite-run-id> UTILITY_SUITE_BUNDLE_OUT=generated/suite.tar.gz
make utility-suite-run-verify UTILITY_SUITE_RUN=<suite-run-id>
```

Suite artifacts are written under `runs/utility-suites/<suite-run-id>/`.
Individual utility-test runs still write normal experiment artifacts under
`runs/<run-id>/`. Suite artifact verification checks `runs.tsv`, `result.json`,
`summary.md`, driver logs, and any linked experiment run artifacts. Suite
bundles preserve both the suite artifact and linked experiment run artifacts in
one tar.gz.
