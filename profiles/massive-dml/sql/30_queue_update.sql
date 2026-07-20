\set ON_ERROR_STOP on

SET client_min_messages = warning;

DROP TABLE IF EXISTS massive_dml.transaction_update_queue;

CREATE TABLE massive_dml.transaction_update_queue (
    id bigint PRIMARY KEY REFERENCES massive_dml.transaction_log(id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done')),
    attempts integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO massive_dml.transaction_update_queue (id)
SELECT id
FROM massive_dml.transaction_log
WHERE (created_ts IS NULL AND created_at IS NOT NULL)
   OR (processed_ts IS NULL AND processed_at IS NOT NULL)
ON CONFLICT DO NOTHING;

CREATE OR REPLACE PROCEDURE massive_dml.backfill_transaction_log_timestamps_from_queue(
    IN p_batch_size integer DEFAULT 5000,
    IN p_sleep_seconds numeric DEFAULT 0
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_grabbed bigint;
    v_rows_updated bigint;
    v_total_grabbed bigint := 0;
    v_total_updated bigint := 0;
    v_batch_no bigint := 0;
BEGIN
    IF p_batch_size <= 0 THEN
        RAISE EXCEPTION 'p_batch_size must be positive';
    END IF;

    IF p_sleep_seconds < 0 THEN
        RAISE EXCEPTION 'p_sleep_seconds must be non-negative';
    END IF;

    LOOP
        v_batch_no := v_batch_no + 1;

        SET LOCAL lock_timeout = '1s';
        SET LOCAL statement_timeout = '10min';
        SET LOCAL TimeZone = 'UTC';

        WITH grabbed AS (
            SELECT id
            FROM massive_dml.transaction_update_queue
            WHERE status = 'pending'
            ORDER BY id
            LIMIT p_batch_size
            FOR UPDATE SKIP LOCKED
        ),
        updated AS (
            UPDATE massive_dml.transaction_log t
            SET created_ts = CASE
                    WHEN t.created_ts IS NULL AND t.created_at IS NOT NULL
                    THEN to_timestamp(t.created_at / 1000000.0)
                    ELSE t.created_ts
                END,
                processed_ts = CASE
                    WHEN t.processed_ts IS NULL AND t.processed_at IS NOT NULL
                    THEN to_timestamp(t.processed_at / 1000000.0)
                    ELSE t.processed_ts
                END
            FROM grabbed g
            WHERE t.id = g.id
              AND (
                    (t.created_ts IS NULL AND t.created_at IS NOT NULL)
                 OR (t.processed_ts IS NULL AND t.processed_at IS NOT NULL)
              )
            RETURNING t.id
        ),
        marked AS (
            UPDATE massive_dml.transaction_update_queue q
            SET status = 'done',
                attempts = q.attempts + 1,
                updated_at = clock_timestamp()
            FROM grabbed g
            WHERE q.id = g.id
            RETURNING q.id
        )
        SELECT
            (SELECT count(*) FROM grabbed),
            (SELECT count(*) FROM updated)
        INTO v_grabbed, v_rows_updated;

        IF v_grabbed = 0 THEN
            RAISE NOTICE 'Finished. Total grabbed: %, total updated: %',
                v_total_grabbed, v_total_updated;
            RETURN;
        END IF;

        v_total_grabbed := v_total_grabbed + v_grabbed;
        v_total_updated := v_total_updated + v_rows_updated;

        RAISE NOTICE 'Batch %, grabbed %, updated %, total grabbed %, total updated %',
            v_batch_no, v_grabbed, v_rows_updated, v_total_grabbed, v_total_updated;

        COMMIT;
        PERFORM pg_sleep(p_sleep_seconds);
    END LOOP;
END;
$$;
