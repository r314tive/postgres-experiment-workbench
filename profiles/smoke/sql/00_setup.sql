\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS smoke CASCADE;
CREATE SCHEMA smoke;

CREATE TABLE smoke.items (
    id bigint PRIMARY KEY,
    group_id integer NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    touched_at timestamptz
);

INSERT INTO smoke.items (id, group_id, payload)
SELECT
    gs,
    (gs % 100)::integer,
    repeat('x', CASE :'profile_size'
        WHEN 'small' THEN 50
        WHEN 'medium' THEN 200
        WHEN 'large' THEN 1000
        ELSE 50
    END)
FROM generate_series(
    1,
    CASE :'profile_size'
        WHEN 'small' THEN 10000
        WHEN 'medium' THEN 100000
        WHEN 'large' THEN 1000000
        ELSE 10000
    END
) AS gs;

CREATE INDEX items_group_id_idx ON smoke.items (group_id);

ANALYZE smoke.items;
