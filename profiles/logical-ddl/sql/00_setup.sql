\set ON_ERROR_STOP on

DROP PUBLICATION IF EXISTS workbench_logical_pub;
DROP SCHEMA IF EXISTS logical_repl CASCADE;
CREATE SCHEMA logical_repl;

CREATE TABLE logical_repl.events (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz
);

CREATE INDEX events_tenant_created_idx
    ON logical_repl.events (tenant_id, created_at DESC);
