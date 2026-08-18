# Benchmark host qualification artifact

`pgworkbench benchmark host-inspect` records a bounded point-in-time host
snapshot. The artifact is **operator-recorded and unsigned**. Its SHA-256
digests detect content changes; they are not a signature, remote attestation,
proof of host identity, or proof that the recorded state is still current.

Without `--strict` and at least one explicit gate, the verdict is always
`unqualified`. Every configured strict gate must pass. If a required
observation is unavailable, that gate fails closed. Client placement is never
inferred: record it explicitly with `--client-placement` and gate it separately
with `--required-client-placement` when it matters.

The snapshot contains portable values and aggregate capacities, not hostnames,
usernames, mount paths, serial numbers, MAC addresses, or environment values.
`--storage-path` selects the filesystem to inspect but only the portable
`--storage-label`, filesystem kind, and aggregate capacity are recorded.

Example:

```bash
pgworkbench benchmark host-inspect --json \
  --output host-qualification.json \
  --storage-path /var/lib/postgresql \
  --storage-label postgres-data \
  --client-placement separate-host \
  --strict \
  --min-logical-cpus 8 \
  --min-memory-available-pct 25 \
  --min-storage-available-pct 30 \
  --max-load-1m-per-cpu 0.5 \
  --required-client-placement separate-host

pgworkbench benchmark host-verify --json host-qualification.json
```

`host-verify` strictly parses the versioned JSON, recomputes its snapshot and
artifact digests, validates derived values, and independently reevaluates the
recorded policy, checks, reasons, and verdict. A successful verification means
only that this unsigned operator record is structurally and internally
consistent. It does not inspect the current host and does not establish who or
what produced the file.
