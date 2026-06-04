\set ON_ERROR_STOP on
\timing on

\echo 'jsonb profile: containment query before jsonb indexes'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM jsonb_lab.events
WHERE doc @> '{"kind": "purchase", "active": true}'::jsonb;

\echo 'jsonb profile: create GIN, expression, and partial indexes'
CREATE INDEX events_doc_path_gin_idx
ON jsonb_lab.events
USING gin (doc jsonb_path_ops);

CREATE INDEX events_kind_region_expr_idx
ON jsonb_lab.events ((doc->>'kind'), (doc->>'region'));

CREATE INDEX events_active_tenant_created_idx
ON jsonb_lab.events (tenant_id, created_at DESC)
WHERE ((doc->>'active')::boolean);

ANALYZE jsonb_lab.events;

\echo 'jsonb profile: containment query after GIN index'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM jsonb_lab.events
WHERE doc @> '{"kind": "purchase", "active": true}'::jsonb;

\echo 'jsonb profile: expression index query'
EXPLAIN (ANALYZE, BUFFERS)
SELECT count(*)
FROM jsonb_lab.events
WHERE doc->>'kind' = 'purchase'
  AND doc->>'region' = 'eu';

\echo 'jsonb profile: partial index query'
EXPLAIN (ANALYZE, BUFFERS)
SELECT id, created_at
FROM jsonb_lab.events
WHERE tenant_id = 42
  AND (doc->>'active')::boolean
ORDER BY created_at DESC
LIMIT 20;

\echo 'jsonb profile: rolled-back jsonb_set update probe'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
UPDATE jsonb_lab.events
SET doc = jsonb_set(doc, '{attrs,reviewed}', 'true'::jsonb, true)
WHERE doc @> '{"kind": "purchase"}'::jsonb;
ROLLBACK;

\echo 'jsonb profile: index sizes and usage counters'
SELECT
    indexrelname,
    idx_scan,
    idx_tup_read,
    idx_tup_fetch,
    pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE schemaname = 'jsonb_lab'
ORDER BY indexrelname;
