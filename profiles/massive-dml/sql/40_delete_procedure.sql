\set ON_ERROR_STOP on

CREATE OR REPLACE PROCEDURE massive_dml.delete_old_audit_records(
    IN p_cutoff timestamptz DEFAULT TIMESTAMPTZ '2026-04-07 00:00:00+00',
    IN p_batch_size integer DEFAULT 3000,
    IN p_sleep_seconds numeric DEFAULT 0
)
LANGUAGE plpgsql
AS $$
DECLARE
    v_rows_deleted bigint;
    v_total_deleted bigint := 0;
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

        WITH target AS (
            SELECT audit_record_id
            FROM massive_dml.audit_record
            WHERE created_at < p_cutoff
            ORDER BY created_at, audit_record_id
            LIMIT p_batch_size
            FOR UPDATE SKIP LOCKED
        )
        DELETE FROM massive_dml.audit_record ar
        USING target t
        WHERE ar.audit_record_id = t.audit_record_id;

        GET DIAGNOSTICS v_rows_deleted = ROW_COUNT;

        IF v_rows_deleted = 0 THEN
            RAISE NOTICE 'Finished. Total deleted: %', v_total_deleted;
            RETURN;
        END IF;

        v_total_deleted := v_total_deleted + v_rows_deleted;
        RAISE NOTICE 'Batch %, deleted %, total %',
            v_batch_no, v_rows_deleted, v_total_deleted;

        COMMIT;
        PERFORM pg_sleep(p_sleep_seconds);
    END LOOP;
END;
$$;
