\set ON_ERROR_STOP on
\timing on

\echo 'temp-spill profile: baseline temp counters'
SELECT
    datname,
    temp_files,
    pg_size_pretty(temp_bytes) AS temp_bytes_pretty,
    temp_bytes
FROM pg_stat_database
WHERE datname = current_database();

\echo 'temp-spill profile: external sort probe'
BEGIN;
SET LOCAL work_mem = '64kB';
EXPLAIN (ANALYZE, BUFFERS, SUMMARY)
SELECT id, group_id, payload
FROM temp_spill.items
ORDER BY payload, id;
ROLLBACK;

\echo 'temp-spill profile: hash aggregate probe'
BEGIN;
SET LOCAL work_mem = '64kB';
EXPLAIN (ANALYZE, BUFFERS, SUMMARY)
SELECT payload, count(*) AS rows_per_payload
FROM temp_spill.items
GROUP BY payload;
ROLLBACK;

SELECT pg_stat_force_next_flush();
SELECT pg_stat_clear_snapshot();

\echo 'temp-spill profile: final temp counters'
SELECT
    datname,
    temp_files,
    pg_size_pretty(temp_bytes) AS temp_bytes_pretty,
    temp_bytes
FROM pg_stat_database
WHERE datname = current_database();
