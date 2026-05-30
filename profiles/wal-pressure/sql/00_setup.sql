\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS wal_pressure CASCADE;
CREATE SCHEMA wal_pressure;

CREATE TABLE wal_pressure.events (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    event_type text NOT NULL,
    payload text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz
);

CREATE INDEX events_tenant_id_idx ON wal_pressure.events (tenant_id);
