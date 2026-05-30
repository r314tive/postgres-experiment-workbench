\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS vacuum_bloat CASCADE;
CREATE SCHEMA vacuum_bloat;

CREATE TABLE vacuum_bloat.events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tenant_id integer NOT NULL,
    status text NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz
) WITH (
    fillfactor = 80,
    autovacuum_enabled = false
);

INSERT INTO vacuum_bloat.events (tenant_id, status, payload)
SELECT
    (gs % 100)::integer,
    CASE
        WHEN gs % 10 = 0 THEN 'archived'
        WHEN gs % 3 = 0 THEN 'pending'
        ELSE 'open'
    END,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 2
        WHEN 'medium' THEN 4
        WHEN 'large' THEN 8
        ELSE 2
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 20000
        WHEN 'medium' THEN 100000
        WHEN 'large' THEN 400000
        ELSE 20000
    END
) AS gs;

CREATE INDEX events_tenant_status_idx ON vacuum_bloat.events (tenant_id, status);

ANALYZE vacuum_bloat.events;
