# When not to use row DELETE

Committed row-delete batches still generate WAL, create dead tuples, consume
I/O, and require vacuum work. They are a fallback, not a universal default.

Prefer partition detach/drop when historical retention aligns with partition
boundaries. Prefer rebuild/swap when most of a table must be rewritten and the
availability trade-off is acceptable. Prefer an application job when
correctness requires external calls, audit events, cache invalidation, domain
retries, or other behavior outside PostgreSQL.

Use row DELETE batches when only a subset is affected, the predicate and index
are clear, gradual cleanup is acceptable, and the operation can be stopped,
resumed, monitored, and vacuumed afterward.

The `massive-dml/partition-drop-vs-delete` experiment demonstrates this choice
with identical source data, correctness assertions, and timing/WAL evidence.
It extends the standalone lab rather than serving as parity evidence for it.
