SELECT
    subname,
    subenabled,
    subslotname,
    subpublications
FROM pg_subscription
ORDER BY subname;

SELECT
    subname,
    pid,
    received_lsn,
    latest_end_lsn,
    latest_end_time
FROM pg_stat_subscription
ORDER BY subname;
