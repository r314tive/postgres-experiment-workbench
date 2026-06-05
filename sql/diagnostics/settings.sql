\pset null '[null]'
\echo 'diagnostic: non-default or pending settings'

SELECT
  name,
  setting,
  unit,
  boot_val,
  reset_val,
  source,
  pending_restart
FROM pg_settings
WHERE
  setting IS DISTINCT FROM boot_val
  OR reset_val IS DISTINCT FROM boot_val
  OR source <> 'default'
  OR pending_restart
ORDER BY name;
