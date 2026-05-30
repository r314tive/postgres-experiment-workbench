\set ON_ERROR_STOP on
\timing on

\echo 'locks profile: holding a row lock for :profile_seconds seconds'
BEGIN;
UPDATE locks.accounts
SET
    note = 'held-by-blocker',
    updated_at = clock_timestamp()
WHERE id = 1
RETURNING id, updated_at;

SELECT
    pg_backend_pid() AS blocker_pid,
    :profile_seconds::integer AS hold_seconds;

SELECT pg_sleep(:profile_seconds::integer);
ROLLBACK;
