\set ON_ERROR_STOP on
\timing on

\echo 'constraints profile: constraint catalog before validation'
SELECT
    conrelid::regclass AS table_name,
    conname,
    contype,
    convalidated,
    condeferrable,
    condeferred
FROM pg_constraint
WHERE connamespace = 'constraints_lab'::regnamespace
ORDER BY conrelid::regclass::text, conname;

\echo 'constraints profile: deferrable foreign key allows child before parent inside transaction'
BEGIN;
SET CONSTRAINTS constraints_lab.orders_customer_fk DEFERRED;
INSERT INTO constraints_lab.orders (id, customer_id, amount, status)
VALUES (900000001, 900000001, 42.00, 'new');
INSERT INTO constraints_lab.customers (id, tenant_id, email, credit_limit)
VALUES (900000001, 1, 'deferred-parent@example.test', 100.00);
COMMIT;

\echo 'constraints profile: expected unique violation is caught'
DO $$
DECLARE
    existing_customer constraints_lab.customers%ROWTYPE;
BEGIN
    SELECT *
    INTO existing_customer
    FROM constraints_lab.customers
    ORDER BY id
    LIMIT 1;

    INSERT INTO constraints_lab.customers (id, tenant_id, email, credit_limit)
    VALUES (
        900000002,
        existing_customer.tenant_id,
        existing_customer.email,
        100.00
    );
EXCEPTION
    WHEN unique_violation THEN
        RAISE NOTICE 'caught expected unique_violation';
END $$;

\echo 'constraints profile: expected check violation is caught'
DO $$
BEGIN
    INSERT INTO constraints_lab.orders (id, customer_id, amount, status)
    VALUES (900000003, 1, -1.00, 'new');
EXCEPTION
    WHEN check_violation THEN
        RAISE NOTICE 'caught expected check_violation';
END $$;

\echo 'constraints profile: NOT VALID constraint fails, then validates after remediation'
DO $$
BEGIN
    ALTER TABLE constraints_lab.status_events
        VALIDATE CONSTRAINT status_events_status_allowed;
EXCEPTION
    WHEN check_violation THEN
        RAISE NOTICE 'caught expected validation failure for existing bad rows';
END $$;

UPDATE constraints_lab.status_events
SET status = 'cancelled'
WHERE status = 'unknown';

ALTER TABLE constraints_lab.status_events
    VALIDATE CONSTRAINT status_events_status_allowed;

\echo 'constraints profile: final validation state'
SELECT
    conrelid::regclass AS table_name,
    conname,
    convalidated,
    pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE connamespace = 'constraints_lab'::regnamespace
ORDER BY conrelid::regclass::text, conname;
