\set ON_ERROR_STOP on
\timing on

\echo 'logical-replication profile: insert/update/delete published rows'
INSERT INTO logical_repl.events (id, tenant_id, payload)
SELECT
    gs,
    (gs % 32)::integer,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 2
        WHEN 'medium' THEN 4
        WHEN 'large' THEN 8
        ELSE 2
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 5000
        WHEN 'medium' THEN 25000
        WHEN 'large' THEN 100000
        ELSE 5000
    END
) AS gs;

UPDATE logical_repl.events
SET
    payload = payload || '-updated',
    updated_at = clock_timestamp()
WHERE id % 11 = 0;

DELETE FROM logical_repl.events
WHERE id % 17 = 0;

ANALYZE logical_repl.events;

\echo 'logical-replication profile: publisher checksum'
SELECT
    count(*) AS rows_after_changes,
    coalesce(sum(id), 0) AS id_sum,
    coalesce(sum(length(payload)), 0) AS payload_bytes,
    coalesce(sum(CASE WHEN updated_at IS NULL THEN 0 ELSE 1 END), 0) AS updated_rows
FROM logical_repl.events;
