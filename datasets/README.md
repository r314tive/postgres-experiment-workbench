# Datasets

Datasets are reusable data-loading specs. They are lower-level than profiles:
a dataset answers "how do I create repeatable data?", while a profile answers
"what PostgreSQL scenario is being demonstrated?"

Run:

```bash
make dataset-list
make dataset-list-go
make dataset-show DATASET_SPEC=synthetic/items
make dataset-show-go DATASET_SPEC=synthetic/items
make dataset-load DATASET_SPEC=synthetic/items DATASET_SIZE=small
```

The Go catalog commands can validate dataset specs without loading data:

```bash
go run ./cmd/pgworkbench dataset validate
```

Supported kinds:

- `sql`: run SQL with `:dataset_schema`, `:dataset_size`, `:dataset_rows`, and
  `:dataset_seed`.
- `profile`: reuse a profile setup as a dataset source.
- `pgbench`: initialize standard pgbench tables.
