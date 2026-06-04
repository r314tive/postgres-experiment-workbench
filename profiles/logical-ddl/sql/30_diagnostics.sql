\set ON_ERROR_STOP on

\echo 'logical-ddl profile: publisher publication tables'
SELECT
    pubname,
    schemaname,
    tablename
FROM pg_publication_tables
WHERE schemaname = 'logical_repl'
ORDER BY pubname, tablename;

\echo 'logical-ddl profile: publisher columns'
SELECT
    table_schema,
    table_name,
    column_name,
    data_type,
    is_nullable,
    column_default
FROM information_schema.columns
WHERE table_schema = 'logical_repl'
  AND table_name IN ('events', 'ddl_notes')
ORDER BY table_name, ordinal_position;

\echo 'logical-ddl profile: publisher checksums'
SELECT
    'events' AS relation_name,
    count(*) AS rows_after_changes,
    coalesce(sum(id), 0) AS id_sum,
    coalesce(sum(length(payload)), 0) AS payload_bytes,
    coalesce(sum(length(ddl_marker)), 0) AS marker_bytes
FROM logical_repl.events
UNION ALL
SELECT
    'ddl_notes' AS relation_name,
    count(*) AS rows_after_changes,
    coalesce(sum(id), 0) AS id_sum,
    coalesce(sum(length(note)), 0) AS payload_bytes,
    0 AS marker_bytes
FROM logical_repl.ddl_notes
ORDER BY relation_name;
