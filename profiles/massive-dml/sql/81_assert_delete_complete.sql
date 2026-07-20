\set ON_ERROR_STOP on

DO $$
DECLARE
    v_total bigint;
    v_old bigint;
BEGIN
    SELECT total_rows, old_rows
    INTO v_total, v_old
    FROM massive_dml.audit_record_delete_stats;

    IF v_old <> 0 THEN
        RAISE EXCEPTION 'expected all rows before the fixed cutoff to be deleted, remaining: %', v_old;
    END IF;

    IF v_total = 0 THEN
        RAISE EXCEPTION 'expected newer audit rows to remain';
    END IF;
END
$$;

SELECT * FROM massive_dml.audit_record_delete_stats;
