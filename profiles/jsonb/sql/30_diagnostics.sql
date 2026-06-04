\set ON_ERROR_STOP on
\pset pager off

\echo 'jsonb profile: document shape counts'
SELECT
    doc->>'kind' AS kind,
    doc->>'region' AS region,
    (doc->>'active')::boolean AS active,
    count(*) AS rows
FROM jsonb_lab.events
GROUP BY 1, 2, 3
ORDER BY rows DESC, kind, region, active
LIMIT 20;

\echo 'jsonb profile: jsonb index diagnostics'
SELECT
    indexrelname,
    idx_scan,
    idx_tup_read,
    idx_tup_fetch,
    pg_size_pretty(pg_relation_size(indexrelid)) AS index_size
FROM pg_stat_user_indexes
WHERE schemaname = 'jsonb_lab'
ORDER BY indexrelname;

\echo 'jsonb profile: JSONB sample paths'
SELECT
    id,
    doc->>'kind' AS kind,
    doc #>> '{attrs,account,tier}' AS account_tier,
    doc #>> '{attrs,device,os}' AS device_os,
    doc->'tags' AS tags
FROM jsonb_lab.events
ORDER BY id
LIMIT 10;
