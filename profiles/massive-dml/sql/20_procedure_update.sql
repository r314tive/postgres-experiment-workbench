\set ON_ERROR_STOP on

CREATE OR REPLACE PROCEDURE massive_dml.backfill_transaction_log_timestamps(
    IN p_batch_size integer DEFAULT 5000,
    IN p_sleep_seconds numeric DEFAULT 0
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_min_id bigint;
    v_max_id bigint;
    v_from_id bigint;
    v_to_id bigint;
    v_rows_updated bigint;
    v_total_updated bigint := 0;
    v_batch_no bigint := 0;
BEGIN
    IF p_batch_size <= 0 THEN
        RAISE EXCEPTION 'p_batch_size must be positive';
    END IF;

    IF p_sleep_seconds < 0 THEN
        RAISE EXCEPTION 'p_sleep_seconds must be non-negative';
    END IF;

    SELECT min(id), max(id)
    INTO v_min_id, v_max_id
    FROM massive_dml.transaction_log
    WHERE (created_ts IS NULL AND created_at IS NOT NULL)
       OR (processed_ts IS NULL AND processed_at IS NOT NULL);

    IF v_min_id IS NULL THEN
        RAISE NOTICE 'Nothing to update';
        RETURN;
    END IF;

    v_from_id := v_min_id;

    WHILE v_from_id <= v_max_id LOOP
        v_to_id := LEAST(v_from_id + p_batch_size - 1, v_max_id);
        v_batch_no := v_batch_no + 1;

        SET LOCAL lock_timeout = '1s';
        SET LOCAL statement_timeout = '10min';
        SET LOCAL TimeZone = 'UTC';

        UPDATE massive_dml.transaction_log
        SET created_ts = CASE
                WHEN created_ts IS NULL AND created_at IS NOT NULL
                THEN to_timestamp(created_at / 1000000.0)
                ELSE created_ts
            END,
            processed_ts = CASE
                WHEN processed_ts IS NULL AND processed_at IS NOT NULL
                THEN to_timestamp(processed_at / 1000000.0)
                ELSE processed_ts
            END
        WHERE id BETWEEN v_from_id AND v_to_id
          AND (
                (created_ts IS NULL AND created_at IS NOT NULL)
             OR (processed_ts IS NULL AND processed_at IS NOT NULL)
          );

        GET DIAGNOSTICS v_rows_updated = ROW_COUNT;
        v_total_updated := v_total_updated + v_rows_updated;

        RAISE NOTICE 'Batch %, id %..%, updated %, total %',
            v_batch_no, v_from_id, v_to_id, v_rows_updated, v_total_updated;

        COMMIT;
        PERFORM pg_sleep(p_sleep_seconds);
        v_from_id := v_to_id + 1;
    END LOOP;

    RAISE NOTICE 'Finished. Total updated: %', v_total_updated;
END;
$$;
