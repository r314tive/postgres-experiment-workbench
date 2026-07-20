# Roadmap

The `v0.1.37` milestone consolidates the standalone massive-DML lab
into the workbench and leave the repository in a release-candidate state. The
platform remains generic; domain SQL, assertions, evidence, and guidance stay
under the `massive-dml` profile.

## v0.1.37 release-candidate scope

### Implemented

- Preserve standalone behavior for generated committed UPDATE/DELETE batches,
  procedure-controlled batches, the queue alternative, and transaction-control
  caveats.
- Preserve the exact generated SQL, parameters, logs, final statistics,
  assertions, metrics, snapshots, and verdict for every run.
- Add physical-strategy experiments for offline bulk load with indexes present,
  offline bulk load with secondary indexes built afterward, and partition
  detach/drop versus row DELETE.
- Keep Compose, PostgreSQL lifecycle, logging, metrics, snapshots, reports,
  comparison, and optional noisia pressure in the workbench platform.
- Cover the profile catalog, workload and experiment plans, matrices, runtime
  assertions, generated artifacts, and run verification in automated tests.

### Release-candidate evidence

- `make check` must pass.
- The standalone lab's Docker-backed tests and the migrated workbench parity
  experiments must both pass before the standalone repository is redirected.
- `make test` and `make release-check` must pass with Docker available.
- `massive-dml-strategy` must pass at `medium` size with three repeats, producing
  matrix summaries plus per-run bulk/partition measurements.
- `make release-snapshot VERSION=0.1.37` must produce archives and a complete
  checksum file.
- The changelog, profile README, demo flow, and release procedure must describe
  commands that were exercised on the candidate tree.

### Deliberately outside the candidate tree

These steps change public history or external systems and happen only after the
candidate commit and CI are green:

1. Commit and push the candidate; require the GitHub `check` workflow to pass.
2. Tag and push `v0.1.37`; verify the GitHub release snapshot and attached
   checksums.
3. Update Confluence and demo materials to the pinned `v0.1.37` profile path.
4. Tag the final standalone version, replace the beginning of its README with a
   move notice, and link the workbench profile and release.
5. Archive the standalone GitHub repository without deleting it.

The old repository stays active until steps 1-3 succeed. Archive is the final
operation, not the migration mechanism.

## Demo contract

The primary demo shows a decision, not a preferred batch size:

1. Use generated committed batches when online row-level changes are required.
2. Compare indexed load with index-after load for an offline bulk load.
3. Compare row DELETE with partition detach/drop when retention boundaries align.
4. Use repeated matrix evidence before drawing performance conclusions.
5. Treat live stop/resume as a manual generated-first scenario inside the same
   profile.

## After v0.1.37

- Add rebuild/swap only when a concrete availability and cutover contract is
  defined.
- Add an application-job example only when external side effects and retry
  semantics are part of the experiment.
- Extract a reusable dataset spec only if another profile needs the same shape;
  the current synthetic schema remains profile-local.
- Move more shell glue to Go only after a stable structured contract exists.

## Platform invariants

- Keep local/disposable defaults and guards against accidental non-local
  PostgreSQL targets.
- Keep every real profile runnable at `PROFILE_SIZE=small`.
- Keep experiment evidence self-contained under `runs/<run-id>/` and free of
  ignored local-only or sensitive material.
- Keep `make check`, Docker-backed tests, artifact scans, privacy scans, and
  release snapshots green before every tag.
- Keep the root README focused on reusable mechanics and domain teaching in the
  owning profile.
