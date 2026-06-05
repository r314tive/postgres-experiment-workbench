\pset null '[null]'
\echo 'diagnostic: activity'

SELECT
  clock_timestamp() AS sampled_at,
  datname,
  pid,
  usename,
  application_name,
  client_addr::text AS client_addr,
  state,
  wait_event_type,
  wait_event,
  age(clock_timestamp(), xact_start) AS xact_age,
  age(clock_timestamp(), query_start) AS query_age,
  left(regexp_replace(query, E'[\\n\\r\\t ]+', ' ', 'g'), 180) AS query
FROM pg_stat_activity
WHERE pid <> pg_backend_pid()
ORDER BY
  COALESCE(xact_start, query_start, backend_start) NULLS LAST,
  pid;
