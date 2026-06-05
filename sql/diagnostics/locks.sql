\pset null '[null]'
\echo 'diagnostic: lock wait graph'

WITH blocked AS (
  SELECT
    activity.pid AS blocked_pid,
    unnest(pg_blocking_pids(activity.pid)) AS blocker_pid
  FROM pg_stat_activity AS activity
  WHERE cardinality(pg_blocking_pids(activity.pid)) > 0
)
SELECT
  blocked.blocked_pid,
  blocked_activity.usename AS blocked_user,
  blocked_activity.state AS blocked_state,
  blocked_activity.wait_event_type AS blocked_wait_type,
  blocked_activity.wait_event AS blocked_wait,
  age(clock_timestamp(), blocked_activity.query_start) AS blocked_query_age,
  left(regexp_replace(blocked_activity.query, E'[\\n\\r\\t ]+', ' ', 'g'), 160) AS blocked_query,
  blocked.blocker_pid,
  blocker_activity.usename AS blocker_user,
  blocker_activity.state AS blocker_state,
  age(clock_timestamp(), blocker_activity.query_start) AS blocker_query_age,
  left(regexp_replace(blocker_activity.query, E'[\\n\\r\\t ]+', ' ', 'g'), 160) AS blocker_query
FROM blocked
JOIN pg_stat_activity AS blocked_activity
  ON blocked_activity.pid = blocked.blocked_pid
LEFT JOIN pg_stat_activity AS blocker_activity
  ON blocker_activity.pid = blocked.blocker_pid
ORDER BY blocked.blocked_pid, blocked.blocker_pid;

\echo 'diagnostic: lock summary'

SELECT
  locktype,
  mode,
  granted,
  count(*) AS locks
FROM pg_locks
GROUP BY locktype, mode, granted
ORDER BY granted, locktype, mode;
