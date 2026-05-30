\set ON_ERROR_STOP on
\timing on

\echo 'indexes profile: query plan before secondary index'
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, amount, created_at
FROM indexes.orders
WHERE tenant_id = 42
  AND status = 'paid'
ORDER BY created_at DESC
LIMIT 20;

\echo 'indexes profile: create targeted composite index'
CREATE INDEX orders_tenant_status_created_idx
ON indexes.orders (tenant_id, status, created_at DESC);

ANALYZE indexes.orders;

\echo 'indexes profile: query plan after secondary index'
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, amount, created_at
FROM indexes.orders
WHERE tenant_id = 42
  AND status = 'paid'
ORDER BY created_at DESC
LIMIT 20;

\echo 'indexes profile: rolled-back insert probe with index maintenance'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
INSERT INTO indexes.orders (id, tenant_id, status, amount, created_at, payload)
SELECT
    1000000000 + gs,
    (gs % 200)::integer,
    'paid',
    42.00,
    clock_timestamp(),
    repeat(md5(gs::text), 2)
FROM generate_series(1, CASE :'profile_size'
    WHEN 'small' THEN 1000
    WHEN 'medium' THEN 5000
    WHEN 'large' THEN 20000
    ELSE 1000
END) AS gs;
ROLLBACK;

\echo 'indexes profile: index sizes and usage counters'
SELECT
    indexrelname,
    idx_scan,
    idx_tup_read,
    idx_tup_fetch,
    pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE schemaname = 'indexes'
ORDER BY indexrelname;
