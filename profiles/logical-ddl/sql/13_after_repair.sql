\set ON_ERROR_STOP on
\timing on

\echo 'logical-ddl profile: write rows after subscriber DDL repair'
INSERT INTO logical_repl.events (id, tenant_id, payload, ddl_marker)
SELECT
    gs,
    (gs % 16)::integer,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'large' THEN 4
        ELSE 1
    END),
    'marker-' || (gs % 7)::text
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 1000
        WHEN 'medium' THEN 5000
        WHEN 'large' THEN 25000
        ELSE 1000
    END
) AS gs;

UPDATE logical_repl.events
SET
    payload = payload || '-updated',
    ddl_marker = ddl_marker || '-updated',
    updated_at = clock_timestamp()
WHERE id % 13 = 0;

DELETE FROM logical_repl.events
WHERE id % 29 = 0;

INSERT INTO logical_repl.ddl_notes (id, note)
SELECT
    gs,
    'note-' || md5(gs::text)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 100
        WHEN 'medium' THEN 500
        WHEN 'large' THEN 2500
        ELSE 100
    END
) AS gs;

UPDATE logical_repl.ddl_notes
SET note = note || '-updated'
WHERE id % 5 = 0;

ANALYZE logical_repl.events;
ANALYZE logical_repl.ddl_notes;

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
