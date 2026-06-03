DROP SUBSCRIPTION IF EXISTS :"subscription_name";

DROP SCHEMA IF EXISTS logical_repl CASCADE;
CREATE SCHEMA logical_repl;

CREATE TABLE logical_repl.events (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL,
    updated_at timestamptz
);

CREATE SUBSCRIPTION :"subscription_name"
CONNECTION :'publisher_conn'
PUBLICATION :"publication_name"
WITH (
    copy_data = true,
    create_slot = true,
    slot_name = :'slot_name'
);

SELECT
    subname,
    subenabled,
    subslotname,
    subpublications
FROM pg_subscription
WHERE subname = :'subscription_name';
