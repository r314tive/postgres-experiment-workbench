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
```

Suite artifacts are written under `runs/utility-suites/<suite-run-id>/`.
Individual utility-test runs still write normal experiment artifacts under
`runs/<run-id>/`.
