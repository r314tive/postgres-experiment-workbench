\set ON_ERROR_STOP on

\echo 'logical-ddl profile: verify DDL did not replicate automatically'
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'logical_repl'
          AND table_name = 'events'
          AND column_name = 'ddl_marker'
    ) THEN
        RAISE EXCEPTION 'subscriber unexpectedly has logical_repl.events.ddl_marker before explicit repair';
    END IF;

    IF to_regclass('logical_repl.ddl_notes') IS NOT NULL THEN
        RAISE EXCEPTION 'subscriber unexpectedly has logical_repl.ddl_notes before explicit repair';
    END IF;
END
$$;
