\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS jsonb_lab CASCADE;
CREATE SCHEMA jsonb_lab;

CREATE TABLE jsonb_lab.events (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    created_at timestamptz NOT NULL,
    doc jsonb NOT NULL
);

WITH profile AS (
    SELECT CASE :'profile_size'
        WHEN 'small' THEN 15000
        WHEN 'medium' THEN 100000
        WHEN 'large' THEN 500000
        ELSE 15000
    END AS event_count
)
INSERT INTO jsonb_lab.events (id, tenant_id, created_at, doc)
SELECT
    gs,
    (gs % 250)::integer,
    clock_timestamp() - ((gs % 60) || ' days')::interval,
    jsonb_build_object(
        'kind', CASE
            WHEN gs % 10 = 0 THEN 'refund'
            WHEN gs % 3 = 0 THEN 'purchase'
            ELSE 'visit'
        END,
        'region', CASE
            WHEN gs % 4 = 0 THEN 'eu'
            WHEN gs % 4 = 1 THEN 'us'
            WHEN gs % 4 = 2 THEN 'apac'
            ELSE 'latam'
        END,
        'active', (gs % 2 = 0),
        'score', (gs % 1000),
        'tags', jsonb_build_array(
            'tenant-' || (gs % 250),
            CASE WHEN gs % 5 = 0 THEN 'priority' ELSE 'standard' END,
            CASE WHEN gs % 7 = 0 THEN 'review' ELSE 'auto' END
        ),
        'attrs', jsonb_build_object(
            'account', jsonb_build_object(
                'tier', CASE
                    WHEN gs % 20 = 0 THEN 'enterprise'
                    WHEN gs % 5 = 0 THEN 'pro'
                    ELSE 'free'
                END
            ),
            'device', jsonb_build_object(
                'os', CASE
                    WHEN gs % 3 = 0 THEN 'linux'
                    WHEN gs % 3 = 1 THEN 'macos'
                    ELSE 'windows'
                END,
                'version', (gs % 15) + 1
            )
        )
    )
FROM profile
CROSS JOIN generate_series(1, profile.event_count) AS gs;

ANALYZE jsonb_lab.events;
