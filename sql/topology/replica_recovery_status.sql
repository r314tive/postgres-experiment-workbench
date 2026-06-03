SELECT
    pg_is_in_recovery() AS in_recovery,
    pg_last_wal_receive_lsn() AS receive_lsn,
    pg_last_wal_replay_lsn() AS replay_lsn,
    CASE
        WHEN pg_last_xact_replay_timestamp() IS NULL THEN NULL
        ELSE clock_timestamp() - pg_last_xact_replay_timestamp()
    END AS replay_delay;
