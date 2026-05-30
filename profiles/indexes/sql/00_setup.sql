\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS indexes CASCADE;
CREATE SCHEMA indexes;

CREATE TABLE indexes.orders (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    status text NOT NULL,
    amount numeric(12, 2) NOT NULL,
    created_at timestamptz NOT NULL,
    payload text NOT NULL
);

INSERT INTO indexes.orders (id, tenant_id, status, amount, created_at, payload)
SELECT
    gs,
    (gs % 200)::integer,
    CASE
        WHEN gs % 20 = 0 THEN 'cancelled'
        WHEN gs % 5 = 0 THEN 'pending'
        ELSE 'paid'
    END,
    ((gs % 10000) / 10.0)::numeric(12, 2),
    clock_timestamp() - ((gs % 90) || ' days')::interval,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'large' THEN 4
        ELSE 1
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 20000
        WHEN 'medium' THEN 100000
        WHEN 'large' THEN 500000
        ELSE 20000
    END
) AS gs;

ANALYZE indexes.orders;
