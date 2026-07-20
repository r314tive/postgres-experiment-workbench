\set ON_ERROR_STOP on

\ir 80_assert_update_complete.sql

DO $$
DECLARE
    v_pending bigint;
    v_done bigint;
BEGIN
    SELECT
        count(*) FILTER (WHERE status = 'pending'),
        count(*) FILTER (WHERE status = 'done')
    INTO v_pending, v_done
    FROM massive_dml.transaction_update_queue;

    IF v_pending <> 0 THEN
        RAISE EXCEPTION 'expected no pending queue rows, found: %', v_pending;
    END IF;

    IF v_done = 0 THEN
        RAISE EXCEPTION 'expected completed queue rows';
    END IF;
END
$$;
