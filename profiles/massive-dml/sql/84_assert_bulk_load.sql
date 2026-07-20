\set ON_ERROR_STOP on

DO $$
DECLARE
    v_result massive_dml.experiment_results%ROWTYPE;
    v_actual_rows bigint;
    v_index_count bigint;
BEGIN
    SELECT * INTO STRICT v_result
    FROM massive_dml.experiment_results
    WHERE scenario = 'offline-bulk-load';

    SELECT count(*) INTO v_actual_rows
    FROM massive_dml.bulk_target;

    SELECT count(*) INTO v_index_count
    FROM pg_indexes
    WHERE schemaname = 'massive_dml'
      AND tablename = 'bulk_target';

    IF v_result.variant NOT IN ('indexed', 'index-after') THEN
        RAISE EXCEPTION 'unexpected bulk-load variant: %', v_result.variant;
    END IF;

    IF v_actual_rows <> v_result.rows_affected THEN
        RAISE EXCEPTION 'bulk-load row count mismatch: table %, result %',
            v_actual_rows, v_result.rows_affected;
    END IF;

    IF v_index_count <> 3 THEN
        RAISE EXCEPTION 'expected primary key and two secondary indexes, found: %',
            v_index_count;
    END IF;

    IF v_result.total_ms < 0 OR v_result.load_ms < 0 OR v_result.index_ms < 0
       OR v_result.wal_bytes < 0 THEN
        RAISE EXCEPTION 'bulk-load result contains negative measurements';
    END IF;
END
$$;
