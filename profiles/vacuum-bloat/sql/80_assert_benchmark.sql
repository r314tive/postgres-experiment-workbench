\set ON_ERROR_STOP on

DO $$
DECLARE
    v_rows bigint;
    v_review bigint;
    v_deleted bigint;
BEGIN
    SELECT count(*), count(*) FILTER (WHERE status = 'review')
    INTO v_rows, v_review
    FROM vacuum_bloat.events;

    SELECT count(*)
    INTO v_deleted
    FROM vacuum_bloat.events
    WHERE id % 13 = 0;

    IF v_rows <= 0 OR v_review <= 0 OR v_deleted <> 0 THEN
        RAISE EXCEPTION
            'vacuum benchmark churn invariant failed: rows=%, review=%, deleted_ids=%',
            v_rows, v_review, v_deleted;
    END IF;
END
$$;
