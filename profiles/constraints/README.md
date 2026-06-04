# constraints

Generic constraint behavior profile.

It creates a disposable schema with:

- primary keys;
- uniqueness scoped by tenant;
- check constraints;
- a deferrable foreign key;
- a `NOT VALID` check constraint that can be validated after remediation.

Useful for testing utilities and PostgreSQL changes that inspect or preserve
constraint metadata, validation state, deferrability, and error behavior.

Run:

```bash
make profile-reset PROFILE=constraints PROFILE_SIZE=small
make profile-run PROFILE=constraints WORKLOAD_SQL=30_diagnostics.sql
make experiment-run EXPERIMENT_SPEC=constraints-validation
```
