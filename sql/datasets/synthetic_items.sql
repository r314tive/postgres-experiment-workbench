\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS :dataset_schema CASCADE;
CREATE SCHEMA :dataset_schema;

CREATE TABLE :dataset_schema.items (
    id bigint PRIMARY KEY,
    group_id integer NOT NULL,
    bucket_id integer NOT NULL,
    amount numeric(12, 2) NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz
);

INSERT INTO :dataset_schema.items (id, group_id, bucket_id, amount, payload)
SELECT
    gs,
    (gs % 100)::integer,
    (gs % 1000)::integer,
    ((gs * :dataset_seed::integer) % 100000 / 100.0)::numeric(12, 2),
    repeat(md5((gs + :dataset_seed::integer)::text), CASE :'dataset_size'
        WHEN 'small' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'large' THEN 4
        ELSE 1
    END)
FROM generate_series(
    1,
    COALESCE(NULLIF(:'dataset_rows', '')::bigint, CASE :'dataset_size'
        WHEN 'small' THEN 10000
        WHEN 'medium' THEN 100000
        WHEN 'large' THEN 1000000
        ELSE 10000
    END)
) AS gs;

CREATE INDEX items_group_id_idx ON :dataset_schema.items (group_id);
CREATE INDEX items_bucket_id_idx ON :dataset_schema.items (bucket_id);

ANALYZE :dataset_schema.items;
