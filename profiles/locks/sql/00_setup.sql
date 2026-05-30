\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS locks CASCADE;
CREATE SCHEMA locks;

CREATE TABLE locks.accounts (
    id integer PRIMARY KEY,
    tenant_id integer NOT NULL,
    balance numeric(12, 2) NOT NULL,
    note text NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

INSERT INTO locks.accounts (id, tenant_id, balance, note)
SELECT
    gs,
    (gs % 10)::integer,
    (1000 + gs)::numeric(12, 2),
    'account-' || gs
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 100
        WHEN 'medium' THEN 1000
        WHEN 'large' THEN 10000
        ELSE 100
    END
) AS gs;

CREATE INDEX accounts_tenant_id_idx ON locks.accounts (tenant_id);

ANALYZE locks.accounts;
