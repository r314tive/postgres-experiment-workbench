# pgdrill Integration Boundary

The projects solve adjacent but different problems:

```text
postgres-experiment-workbench
  scenario + dataset + workload + experiment evidence
                         |
                         | reviewed, versioned baseline export
                         v
pgdrill
  provider restore + probes + policy + cleanup + assurance report
```

They remain separate repositories, binaries, licenses, release candidates, and
evidence schemas. Workbench must not absorb backup-provider discovery or restore
lifecycle. pgdrill must not execute arbitrary workbench shell hooks or reinterpret
benchmark evidence as recovery assurance.

The implemented `pgworkbench.pgdrill-baseline/v1` bridge contains only:

- scenario-pack id/version/digest;
- experiment spec id/digest;
- immutable dataset/baseline provenance;
- a reviewed read-only SQL predicate and expected recovery boundary.

It must exclude credentials, connection secrets, arbitrary shell, mutable host
paths, provider configuration, benchmark conclusions, and claims about backup
validity, restore success, RPO, RTO, or SLA.

```bash
# Prefer a complete relocated run bundle when handing provenance across a
# repository boundary.
pgworkbench bridge pgdrill export --bundle \
  <extracted-run-dir> generated/pgdrill-baseline.json

# Optional SQL is read from a reviewed file and is never executed here.
pgworkbench bridge pgdrill export --bundle \
  --reviewed-predicate-file recovery-boundary.sql \
  <extracted-run-dir> generated/pgdrill-baseline.json

pgworkbench bridge pgdrill verify generated/pgdrill-baseline.json
pgworkbench bridge pgdrill verify \
  --source <extracted-run-dir> generated/pgdrill-baseline.json
```

Export first requires an ordinary versioned `passed` experiment and reruns the
normal or complete-bundle verifier. It binds the manifest, both verdict files,
scenario pack, experiment spec, runtime, and observed PostgreSQL version by
portable identities and digests. The output is create-once, unsigned,
operator-recorded baseline provenance. Standalone `verify` checks its closed
schema and self-consistency; `verify --source` additionally re-verifies and
re-derives every field from the supplied run.

The optional predicate is explicitly marked human-reviewed and future-consumer
executed. Workbench applies a conservative lexical guard, but it is not a SQL
semantic proof: pgdrill or another consumer must parse/validate it again and
execute it only against the isolated recovered target. This bridge does not
modify pgdrill and is not a pgdrill report.
