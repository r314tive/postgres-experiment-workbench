\set ON_ERROR_STOP on

DROP SCHEMA IF EXISTS constraints_lab CASCADE;
CREATE SCHEMA constraints_lab;

CREATE TABLE constraints_lab.customers (
    id bigint PRIMARY KEY,
    tenant_id integer NOT NULL,
    email text NOT NULL,
    credit_limit numeric(12, 2) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT customers_credit_limit_nonnegative CHECK (credit_limit >= 0),
    CONSTRAINT customers_tenant_email_key UNIQUE (tenant_id, email)
);

CREATE TABLE constraints_lab.orders (
    id bigint PRIMARY KEY,
    customer_id bigint NOT NULL,
    amount numeric(12, 2) NOT NULL,
    status text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT orders_amount_positive CHECK (amount > 0),
    CONSTRAINT orders_status_allowed CHECK (status IN ('new', 'paid', 'cancelled')),
    CONSTRAINT orders_customer_fk FOREIGN KEY (customer_id)
        REFERENCES constraints_lab.customers (id)
        DEFERRABLE INITIALLY IMMEDIATE
);

WITH profile AS (
    SELECT CASE :'profile_size'
        WHEN 'small' THEN 1000
        WHEN 'medium' THEN 10000
        WHEN 'large' THEN 100000
        ELSE 1000
    END AS customer_count
)
INSERT INTO constraints_lab.customers (id, tenant_id, email, credit_limit)
SELECT
    gs,
    (gs % 100)::integer,
    'customer-' || gs || '@example.test',
    ((gs % 5000) + 100)::numeric(12, 2)
FROM profile
CROSS JOIN generate_series(1, profile.customer_count) AS gs;

WITH profile AS (
    SELECT CASE :'profile_size'
        WHEN 'small' THEN 5000
        WHEN 'medium' THEN 50000
        WHEN 'large' THEN 500000
        ELSE 5000
    END AS order_count,
    CASE :'profile_size'
        WHEN 'small' THEN 1000
        WHEN 'medium' THEN 10000
        WHEN 'large' THEN 100000
        ELSE 1000
    END AS customer_count
)
INSERT INTO constraints_lab.orders (id, customer_id, amount, status, created_at)
SELECT
    gs,
    ((gs - 1) % profile.customer_count) + 1,
    ((gs % 10000) / 10.0 + 1)::numeric(12, 2),
    CASE
        WHEN gs % 20 = 0 THEN 'cancelled'
        WHEN gs % 3 = 0 THEN 'paid'
        ELSE 'new'
    END,
    clock_timestamp() - ((gs % 30) || ' days')::interval
FROM profile
CROSS JOIN generate_series(1, profile.order_count) AS gs;

CREATE TABLE constraints_lab.status_events (
    id bigint PRIMARY KEY,
    status text NOT NULL,
    payload text NOT NULL
);

INSERT INTO constraints_lab.status_events (id, status, payload)
VALUES
    (1, 'new', 'valid row'),
    (2, 'paid', 'valid row'),
    (3, 'unknown', 'existing bad row kept for NOT VALID demonstration');

ALTER TABLE constraints_lab.status_events
    ADD CONSTRAINT status_events_status_allowed
    CHECK (status IN ('new', 'paid', 'cancelled')) NOT VALID;

ANALYZE constraints_lab.customers;
ANALYZE constraints_lab.orders;
ANALYZE constraints_lab.status_events;
