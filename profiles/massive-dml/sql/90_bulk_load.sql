\set ON_ERROR_STOP on

\if :{?bulk_mode}
\else
\set bulk_mode indexed
\endif

\if :{?bulk_rows}
\else
\set bulk_rows 20000
\endif

\if :{?bulk_payload_bytes}
\else
\set bulk_payload_bytes 64
\endif

SELECT set_config('massive_dml.bulk_mode', :'bulk_mode', false);
SELECT set_config('massive_dml.bulk_rows', :'bulk_rows', false);
SELECT set_config('massive_dml.bulk_payload_bytes', :'bulk_payload_bytes', false);

DO $$
DECLARE
    v_mode text := current_setting('massive_dml.bulk_mode');
    v_rows bigint := current_setting('massive_dml.bulk_rows')::bigint;
    v_payload_bytes integer := current_setting('massive_dml.bulk_payload_bytes')::integer;
    v_total_start timestamptz;
    v_load_start timestamptz;
    v_load_end timestamptz;
    v_index_start timestamptz;
    v_total_end timestamptz;
    v_wal_start pg_lsn;
    v_wal_end pg_lsn;
    v_load_ms numeric;
    v_index_ms numeric;
BEGIN
    IF v_mode NOT IN ('indexed', 'index-after') THEN
        RAISE EXCEPTION 'unsupported bulk_mode: %', v_mode;
    END IF;

    IF v_rows <= 0 OR v_payload_bytes <= 0 THEN
        RAISE EXCEPTION 'bulk_rows and bulk_payload_bytes must be positive';
    END IF;

    DELETE FROM massive_dml.experiment_results
    WHERE scenario = 'offline-bulk-load';

    DROP TABLE IF EXISTS massive_dml.bulk_target;

    v_total_start := clock_timestamp();
    v_wal_start := pg_current_wal_insert_lsn();

    CREATE TABLE massive_dml.bulk_target (
        id bigint PRIMARY KEY,
        tenant_id integer NOT NULL,
        occurred_at timestamptz NOT NULL,
        payload text NOT NULL
    );

    IF v_mode = 'indexed' THEN
        v_index_start := clock_timestamp();
        CREATE INDEX bulk_target_tenant_occurred_idx
            ON massive_dml.bulk_target (tenant_id, occurred_at);
        CREATE INDEX bulk_target_occurred_idx
            ON massive_dml.bulk_target (occurred_at);
        v_load_start := clock_timestamp();
        v_index_ms := extract(epoch FROM (v_load_start - v_index_start)) * 1000;
    ELSE
        v_load_start := clock_timestamp();
        v_index_ms := 0;
    END IF;

    INSERT INTO massive_dml.bulk_target (id, tenant_id, occurred_at, payload)
    SELECT
        gs,
        (gs % 1000)::integer,
        TIMESTAMPTZ '2026-01-01 00:00:00+00' + make_interval(secs => gs),
        left(repeat(md5(gs::text), (v_payload_bytes + 31) / 32), v_payload_bytes)
    FROM generate_series(1, v_rows) AS gs;

    v_load_end := clock_timestamp();
    v_load_ms := extract(epoch FROM (v_load_end - v_load_start)) * 1000;

    IF v_mode = 'index-after' THEN
        v_index_start := clock_timestamp();
        CREATE INDEX bulk_target_tenant_occurred_idx
            ON massive_dml.bulk_target (tenant_id, occurred_at);
        CREATE INDEX bulk_target_occurred_idx
            ON massive_dml.bulk_target (occurred_at);
        v_total_end := clock_timestamp();
        v_index_ms := extract(epoch FROM (v_total_end - v_index_start)) * 1000;
    ELSE
        v_total_end := clock_timestamp();
    END IF;

    ANALYZE massive_dml.bulk_target;
    v_wal_end := pg_current_wal_insert_lsn();

    INSERT INTO massive_dml.experiment_results (
        scenario,
        variant,
        rows_affected,
        load_ms,
        index_ms,
        total_ms,
        wal_bytes,
        table_bytes,
        index_bytes
    )
    VALUES (
        'offline-bulk-load',
        v_mode,
        v_rows,
        round(v_load_ms, 3),
        round(v_index_ms, 3),
        round(extract(epoch FROM (v_total_end - v_total_start)) * 1000, 3),
        pg_wal_lsn_diff(v_wal_end, v_wal_start),
        pg_table_size('massive_dml.bulk_target'),
        pg_indexes_size('massive_dml.bulk_target')
    );
END
$$;

SELECT *
FROM massive_dml.experiment_results
WHERE scenario = 'offline-bulk-load';
