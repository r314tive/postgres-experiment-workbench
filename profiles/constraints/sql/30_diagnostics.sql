\set ON_ERROR_STOP on
\pset pager off

\echo 'constraints profile: pg_constraint diagnostics'
SELECT
    conrelid::regclass AS table_name,
    conname,
    contype,
    convalidated,
    condeferrable,
    condeferred,
    pg_get_constraintdef(oid) AS definition
FROM pg_constraint
WHERE connamespace = 'constraints_lab'::regnamespace
ORDER BY conrelid::regclass::text, conname;

\echo 'constraints profile: table row counts'
SELECT 'customers' AS table_name, count(*) AS rows FROM constraints_lab.customers
UNION ALL
SELECT 'orders' AS table_name, count(*) AS rows FROM constraints_lab.orders
UNION ALL
SELECT 'status_events' AS table_name, count(*) AS rows FROM constraints_lab.status_events
ORDER BY table_name;
