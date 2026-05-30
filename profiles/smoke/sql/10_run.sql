\set ON_ERROR_STOP on
\timing on

\echo 'smoke profile: representative SELECT'
SELECT group_id, count(*) AS rows_per_group
FROM smoke.items
GROUP BY group_id
ORDER BY group_id
LIMIT 10;

\echo 'smoke profile: representative UPDATE inside rollback'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
UPDATE smoke.items
SET touched_at = clock_timestamp()
WHERE group_id = 42;
ROLLBACK;
