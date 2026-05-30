\set ON_ERROR_STOP on
\timing on

\echo 'partitioning profile: existing partitions'
SELECT
    child.relname AS partition_name,
    pg_size_pretty(pg_total_relation_size(child.oid)) AS total_size
FROM pg_inherits i
JOIN pg_class parent ON parent.oid = i.inhparent
JOIN pg_class child ON child.oid = i.inhrelid
JOIN pg_namespace nsp ON nsp.oid = parent.relnamespace
WHERE nsp.nspname = 'partitioning'
  AND parent.relname = 'events'
ORDER BY child.relname;

\echo 'partitioning profile: pruning for March date range'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM partitioning.events
WHERE occurred_on >= DATE '2025-03-01'
  AND occurred_on < DATE '2025-04-01'
  AND tenant_id = 7;

\echo 'partitioning profile: stage and attach a May partition'
CREATE TABLE partitioning.events_2025_05 (
    LIKE partitioning.events INCLUDING DEFAULTS INCLUDING CONSTRAINTS
);

ALTER TABLE partitioning.events_2025_05
ADD CONSTRAINT events_2025_05_range
CHECK (occurred_on >= DATE '2025-05-01' AND occurred_on < DATE '2025-06-01');

INSERT INTO partitioning.events_2025_05 (id, occurred_on, tenant_id, event_type, payload)
SELECT
    100000000 + gs,
    DATE '2025-05-01' + ((gs - 1) % 31),
    (gs % 50)::integer,
    'staged',
    repeat(md5(gs::text), 1)
FROM generate_series(1, CASE :'profile_size'
    WHEN 'small' THEN 3100
    WHEN 'medium' THEN 15500
    WHEN 'large' THEN 62000
    ELSE 3100
END) AS gs;

ALTER TABLE partitioning.events
ATTACH PARTITION partitioning.events_2025_05
FOR VALUES FROM ('2025-05-01') TO ('2025-06-01');

ANALYZE partitioning.events;

\echo 'partitioning profile: pruning after attach'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM partitioning.events
WHERE occurred_on >= DATE '2025-05-01'
  AND occurred_on < DATE '2025-06-01'
  AND tenant_id = 7;

\echo 'partitioning profile: detach and drop staged partition'
ALTER TABLE partitioning.events
DETACH PARTITION partitioning.events_2025_05;

DROP TABLE partitioning.events_2025_05;

SELECT count(*) AS remaining_parent_rows
FROM partitioning.events;
