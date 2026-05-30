\set ON_ERROR_STOP on
\timing on

\echo 'locks profile: attempting row update with a short lock_timeout'
DO $$
BEGIN
    PERFORM set_config('lock_timeout', '3s', true);

    UPDATE locks.accounts
    SET
        balance = balance + 10,
        updated_at = clock_timestamp()
    WHERE id = 1;

    RAISE NOTICE 'waiter acquired the row lock before lock_timeout';
EXCEPTION
    WHEN lock_not_available THEN
        RAISE NOTICE 'waiter observed lock_timeout while waiting for row id=1';
END
$$;
