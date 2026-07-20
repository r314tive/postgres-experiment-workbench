\set ON_ERROR_STOP on

\if :{?partition_rows}
\else
\set partition_rows 20000
\endif

\if :{?partition_old_rows}
\else
\set partition_old_rows 8000
\endif

\if :{?partition_payload_bytes}
\else
\set partition_payload_bytes 64
\endif

SELECT set_config('massive_dml.partition_rows', :'partition_rows', false);
SELECT set_config('massive_dml.partition_old_rows', :'partition_old_rows', false);
SELECT set_config('massive_dml.partition_payload_bytes', :'partition_payload_bytes', false);

DROP TABLE IF EXISTS massive_dml.row_delete_events CASCADE;
DROP TABLE IF EXISTS massive_dml.partition_drop_events CASCADE;

CREATE TABLE massive_dml.row_delete_events (
    id bigint NOT NULL,
    occurred_on date NOT NULL,
    payload text NOT NULL,
    PRIMARY KEY (occurred_on, id)
) PARTITION BY RANGE (occurred_on);

CREATE TABLE massive_dml.row_delete_events_old
    PARTITION OF massive_dml.row_delete_events
    FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE massive_dml.row_delete_events_new
    PARTITION OF massive_dml.row_delete_events
    FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');

CREATE TABLE massive_dml.partition_drop_events (
    id bigint NOT NULL,
    occurred_on date NOT NULL,
    payload text NOT NULL,
    PRIMARY KEY (occurred_on, id)
) PARTITION BY RANGE (occurred_on);

CREATE TABLE massive_dml.partition_drop_events_old
    PARTITION OF massive_dml.partition_drop_events
    FOR VALUES FROM ('2026-01-01') TO ('2026-02-01');
CREATE TABLE massive_dml.partition_drop_events_new
    PARTITION OF massive_dml.partition_drop_events
    FOR VALUES FROM ('2026-02-01') TO ('2026-03-01');

WITH settings AS (
    SELECT
        current_setting('massive_dml.partition_rows')::bigint AS total_rows,
        current_setting('massive_dml.partition_old_rows')::bigint AS old_rows,
        current_setting('massive_dml.partition_payload_bytes')::integer AS payload_bytes
)
INSERT INTO massive_dml.row_delete_events (id, occurred_on, payload)
SELECT
    gs,
    CASE
        WHEN gs <= settings.old_rows THEN DATE '2026-01-01' + ((gs - 1) % 31)::integer
        ELSE DATE '2026-02-01' + ((gs - settings.old_rows - 1) % 28)::integer
    END,
    left(repeat(md5(gs::text), (settings.payload_bytes + 31) / 32), settings.payload_bytes)
FROM settings
CROSS JOIN LATERAL generate_series(1, settings.total_rows) AS gs;

INSERT INTO massive_dml.partition_drop_events
SELECT * FROM massive_dml.row_delete_events;

ANALYZE massive_dml.row_delete_events;
ANALYZE massive_dml.partition_drop_events;

DO $$
DECLARE
    v_delete_start timestamptz;
    v_delete_end timestamptz;
    v_drop_start timestamptz;
    v_drop_end timestamptz;
    v_wal_start pg_lsn;
    v_wal_end pg_lsn;
    v_delete_rows bigint;
    v_drop_rows bigint;
    v_table_bytes bigint;
    v_index_bytes bigint;
BEGIN
    DELETE FROM massive_dml.experiment_results
    WHERE scenario = 'partition-remove';

    v_wal_start := pg_current_wal_insert_lsn();
    v_delete_start := clock_timestamp();
    DELETE FROM massive_dml.row_delete_events
    WHERE occurred_on < DATE '2026-02-01';
    GET DIAGNOSTICS v_delete_rows = ROW_COUNT;
    v_delete_end := clock_timestamp();
    v_wal_end := pg_current_wal_insert_lsn();

    SELECT
        coalesce(sum(pg_table_size(relid)), 0),
        coalesce(sum(pg_indexes_size(relid)), 0)
    INTO v_table_bytes, v_index_bytes
    FROM pg_partition_tree('massive_dml.row_delete_events')
    WHERE isleaf;

    INSERT INTO massive_dml.experiment_results (
        scenario, variant, rows_affected, load_ms, index_ms, total_ms,
        wal_bytes, table_bytes, index_bytes
    )
    VALUES (
        'partition-remove',
        'row-delete',
        v_delete_rows,
        NULL,
        NULL,
        round(extract(epoch FROM (v_delete_end - v_delete_start)) * 1000, 3),
        pg_wal_lsn_diff(v_wal_end, v_wal_start),
        v_table_bytes,
        v_index_bytes
    );

    SELECT count(*) INTO v_drop_rows
    FROM massive_dml.partition_drop_events_old;

    v_wal_start := pg_current_wal_insert_lsn();
    v_drop_start := clock_timestamp();
    ALTER TABLE massive_dml.partition_drop_events
        DETACH PARTITION massive_dml.partition_drop_events_old;
    DROP TABLE massive_dml.partition_drop_events_old;
    v_drop_end := clock_timestamp();
    v_wal_end := pg_current_wal_insert_lsn();

    SELECT
        coalesce(sum(pg_table_size(relid)), 0),
        coalesce(sum(pg_indexes_size(relid)), 0)
    INTO v_table_bytes, v_index_bytes
    FROM pg_partition_tree('massive_dml.partition_drop_events')
    WHERE isleaf;

    INSERT INTO massive_dml.experiment_results (
        scenario, variant, rows_affected, load_ms, index_ms, total_ms,
        wal_bytes, table_bytes, index_bytes
    )
    VALUES (
        'partition-remove',
        'partition-drop',
        v_drop_rows,
        NULL,
        NULL,
        round(extract(epoch FROM (v_drop_end - v_drop_start)) * 1000, 3),
        pg_wal_lsn_diff(v_wal_end, v_wal_start),
        v_table_bytes,
        v_index_bytes
    );
END
$$;

SELECT *
FROM massive_dml.experiment_results
WHERE scenario = 'partition-remove'
ORDER BY variant;
