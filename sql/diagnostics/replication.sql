\pset null '[null]'
\echo 'diagnostic: primary replication senders'

SELECT
  pid,
  usename,
  application_name,
  client_addr::text AS client_addr,
  state,
  sync_state,
  sent_lsn,
  write_lsn,
  flush_lsn,
  replay_lsn,
  pg_size_pretty(pg_wal_lsn_diff(sent_lsn, replay_lsn)) AS sent_replay_lag,
  write_lag,
  flush_lag,
  replay_lag
FROM pg_stat_replication
ORDER BY application_name, pid;

\echo 'diagnostic: replication slots'

SELECT
  slot_name,
  plugin,
  slot_type,
  database,
  active,
  restart_lsn,
  confirmed_flush_lsn
FROM pg_replication_slots
ORDER BY slot_name;
