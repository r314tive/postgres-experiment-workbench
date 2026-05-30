\set ON_ERROR_STOP on
\timing on

\echo 'locks profile: table lock held only inside this transaction'
BEGIN;
LOCK TABLE locks.accounts IN SHARE ROW EXCLUSIVE MODE;
SELECT
    a.pid,
    l.locktype,
    l.mode,
    l.granted,
    l.relation::regclass AS relation
FROM pg_locks l
JOIN pg_stat_activity a ON a.pid = l.pid
WHERE a.pid = pg_backend_pid()
  AND l.relation = 'locks.accounts'::regclass
ORDER BY l.mode;
ROLLBACK;

\echo 'locks profile: advisory lock visibility'
BEGIN;
SELECT pg_advisory_lock(hashtext('locks.profile.demo'));
SELECT
    locktype,
    mode,
    granted,
    objid
FROM pg_locks
WHERE pid = pg_backend_pid()
  AND locktype = 'advisory'
ORDER BY mode;
SELECT pg_advisory_unlock(hashtext('locks.profile.demo'));
COMMIT;

\echo 'locks profile: representative row update plan rolled back'
BEGIN;
EXPLAIN (ANALYZE, BUFFERS)
UPDATE locks.accounts
SET
    balance = balance + 1,
    updated_at = clock_timestamp()
WHERE id = 1;
ROLLBACK;
