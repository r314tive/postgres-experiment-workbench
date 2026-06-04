\set ON_ERROR_STOP on
\timing on

\echo 'logical-ddl profile: apply publisher-side DDL'
ALTER TABLE logical_repl.events
    ADD COLUMN IF NOT EXISTS ddl_marker text DEFAULT 'publisher-ddl' NOT NULL;

CREATE INDEX IF NOT EXISTS events_ddl_marker_idx
    ON logical_repl.events (ddl_marker);

CREATE TABLE IF NOT EXISTS logical_repl.ddl_notes (
    id bigint PRIMARY KEY,
    note text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_publication_tables
        WHERE pubname = 'workbench_logical_pub'
          AND schemaname = 'logical_repl'
          AND tablename = 'ddl_notes'
    ) THEN
        EXECUTE 'ALTER PUBLICATION workbench_logical_pub ADD TABLE logical_repl.ddl_notes';
    END IF;
END
$$;

\echo 'logical-ddl profile: publisher DDL state'
SELECT
    table_schema,
    table_name,
    column_name,
    data_type
FROM information_schema.columns
WHERE table_schema = 'logical_repl'
  AND table_name IN ('events', 'ddl_notes')
ORDER BY table_name, ordinal_position;

SELECT
    pubname,
    schemaname,
    tablename
FROM pg_publication_tables
WHERE pubname = 'workbench_logical_pub'
ORDER BY schemaname, tablename;
