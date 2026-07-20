\set ON_ERROR_STOP on

DO $$
DECLARE
    v_delete massive_dml.experiment_results%ROWTYPE;
    v_drop massive_dml.experiment_results%ROWTYPE;
    v_delete_remaining bigint;
    v_drop_remaining bigint;
    v_delete_old bigint;
    v_drop_old bigint;
BEGIN
    SELECT * INTO STRICT v_delete
    FROM massive_dml.experiment_results
    WHERE scenario = 'partition-remove' AND variant = 'row-delete';

    SELECT * INTO STRICT v_drop
    FROM massive_dml.experiment_results
    WHERE scenario = 'partition-remove' AND variant = 'partition-drop';

    SELECT count(*), count(*) FILTER (WHERE occurred_on < DATE '2026-02-01')
    INTO v_delete_remaining, v_delete_old
    FROM massive_dml.row_delete_events;

    SELECT count(*), count(*) FILTER (WHERE occurred_on < DATE '2026-02-01')
    INTO v_drop_remaining, v_drop_old
    FROM massive_dml.partition_drop_events;

    IF v_delete.rows_affected <> v_drop.rows_affected OR v_delete.rows_affected = 0 THEN
        RAISE EXCEPTION 'partition strategies removed different row counts: delete %, drop %',
            v_delete.rows_affected, v_drop.rows_affected;
    END IF;

    IF v_delete_remaining <> v_drop_remaining OR v_delete_remaining = 0 THEN
        RAISE EXCEPTION 'partition strategies retained different row counts: delete %, drop %',
            v_delete_remaining, v_drop_remaining;
    END IF;

    IF v_delete_old <> 0 OR v_drop_old <> 0 THEN
        RAISE EXCEPTION 'old rows remain after partition comparison';
    END IF;

    IF v_delete.total_ms < 0 OR v_drop.total_ms < 0
       OR v_delete.wal_bytes < 0 OR v_drop.wal_bytes < 0 THEN
        RAISE EXCEPTION 'partition result contains negative measurements';
    END IF;
END
$$;
