# temp-spill

Creates a bounded dataset for sort and hash aggregate spills, then runs low
`work_mem` probes that expose temporary file behavior through plans and
database counters.

Run:

```bash
make profile-reset PROFILE=temp-spill PROFILE_SIZE=small
```

Useful follow-up:

```bash
METRICS_DURATION=10 METRICS_INTERVAL=1 make metrics-sample
make monitor
```
