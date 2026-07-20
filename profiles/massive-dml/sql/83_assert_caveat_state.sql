\set ON_ERROR_STOP on

DO $$
DECLARE
    v_remaining bigint;
    v_invalid_created bigint;
    v_invalid_processed bigint;
BEGIN
    SELECT
        backfillable_remaining,
        invalid_created_backfills,
        invalid_processed_backfills
    INTO v_remaining, v_invalid_created, v_invalid_processed
    FROM massive_dml.transaction_log_backfill_stats;

    IF v_remaining = 0 THEN
        RAISE EXCEPTION 'expected failed caveat calls to leave backfillable work';
    END IF;

    IF v_invalid_created <> 0 OR v_invalid_processed <> 0 THEN
        RAISE EXCEPTION 'failed caveat calls corrupted source-null controls';
    END IF;
END
$$;
