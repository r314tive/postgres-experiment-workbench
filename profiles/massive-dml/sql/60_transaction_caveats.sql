\set ON_ERROR_STOP on

CREATE OR REPLACE PROCEDURE massive_dml.bad_temp_table_on_commit_drop_demo()
LANGUAGE plpgsql
AS $$
DECLARE
    v_id bigint;
BEGIN
    CREATE TEMP TABLE tmp_massive_dml_ids(id bigint PRIMARY KEY) ON COMMIT DROP;

    INSERT INTO tmp_massive_dml_ids(id)
    SELECT id
    FROM massive_dml.transaction_log
    WHERE (created_ts IS NULL AND created_at IS NOT NULL)
       OR (processed_ts IS NULL AND processed_at IS NOT NULL)
    ORDER BY id
    LIMIT 10;

    COMMIT;

    SELECT id
    INTO v_id
    FROM tmp_massive_dml_ids
    ORDER BY id
    LIMIT 1;

    RAISE NOTICE 'This notice should not be reached: %', v_id;
END;
$$;
