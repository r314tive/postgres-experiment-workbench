\set ON_ERROR_STOP on

\echo 'logical-ddl profile: apply explicit subscriber DDL repair'
ALTER TABLE logical_repl.events
    ADD COLUMN IF NOT EXISTS ddl_marker text DEFAULT 'publisher-ddl' NOT NULL;

CREATE INDEX IF NOT EXISTS events_ddl_marker_idx
    ON logical_repl.events (ddl_marker);

CREATE TABLE IF NOT EXISTS logical_repl.ddl_notes (
    id bigint PRIMARY KEY,
    note text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

ALTER SUBSCRIPTION workbench_logical_sub
    REFRESH PUBLICATION WITH (copy_data = true);

SELECT
    table_schema,
    table_name,
    column_name,
    data_type
FROM information_schema.columns
WHERE table_schema = 'logical_repl'
  AND table_name IN ('events', 'ddl_notes')
ORDER BY table_name, ordinal_position;
