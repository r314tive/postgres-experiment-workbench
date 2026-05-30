\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS partitioning CASCADE;
CREATE SCHEMA partitioning;

CREATE TABLE partitioning.events (
    id bigint NOT NULL,
    occurred_on date NOT NULL,
    tenant_id integer NOT NULL,
    event_type text NOT NULL,
    payload text NOT NULL,
    PRIMARY KEY (id, occurred_on)
) PARTITION BY RANGE (occurred_on);

CREATE TABLE partitioning.events_2025_01
    PARTITION OF partitioning.events
    FOR VALUES FROM ('2025-01-01') TO ('2025-02-01');

CREATE TABLE partitioning.events_2025_02
    PARTITION OF partitioning.events
    FOR VALUES FROM ('2025-02-01') TO ('2025-03-01');

CREATE TABLE partitioning.events_2025_03
    PARTITION OF partitioning.events
    FOR VALUES FROM ('2025-03-01') TO ('2025-04-01');

CREATE TABLE partitioning.events_2025_04
    PARTITION OF partitioning.events
    FOR VALUES FROM ('2025-04-01') TO ('2025-05-01');

INSERT INTO partitioning.events (id, occurred_on, tenant_id, event_type, payload)
SELECT
    gs,
    DATE '2025-01-01' + ((gs - 1) % 120),
    (gs % 50)::integer,
    CASE WHEN gs % 2 = 0 THEN 'click' ELSE 'view' END,
    repeat(md5(gs::text), CASE :'profile_size'
        WHEN 'small' THEN 1
        WHEN 'medium' THEN 2
        WHEN 'large' THEN 4
        ELSE 1
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 12000
        WHEN 'medium' THEN 60000
        WHEN 'large' THEN 240000
        ELSE 12000
    END
) AS gs;

CREATE INDEX events_tenant_occurred_idx
ON partitioning.events (tenant_id, occurred_on);

ANALYZE partitioning.events;
