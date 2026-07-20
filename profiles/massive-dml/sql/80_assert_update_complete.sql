\set ON_ERROR_STOP on

DO $$
DECLARE
    v_remaining bigint;
    v_invalid_created bigint;
    v_invalid_processed bigint;
    v_created_source_null bigint;
    v_processed_source_null bigint;
BEGIN
    SELECT
        backfillable_remaining,
        invalid_created_backfills,
        invalid_processed_backfills,
        created_source_null_rows,
        processed_source_null_rows
    INTO
        v_remaining,
        v_invalid_created,
        v_invalid_processed,
        v_created_source_null,
        v_processed_source_null
    FROM massive_dml.transaction_log_backfill_stats;

    IF v_remaining <> 0 THEN
        RAISE EXCEPTION 'expected completed backfill, remaining rows: %', v_remaining;
    END IF;

    IF v_invalid_created <> 0 OR v_invalid_processed <> 0 THEN
        RAISE EXCEPTION 'source-null values were backfilled: created %, processed %',
            v_invalid_created, v_invalid_processed;
    END IF;

    IF v_created_source_null = 0 OR v_processed_source_null = 0 THEN
        RAISE EXCEPTION 'synthetic source-null control rows are missing';
    END IF;
END
$$;

SELECT * FROM massive_dml.transaction_log_backfill_stats;
