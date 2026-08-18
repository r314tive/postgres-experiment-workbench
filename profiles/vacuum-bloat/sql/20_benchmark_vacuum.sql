\set ON_ERROR_STOP on

SELECT clock_timestamp() AS benchmark_started_at
\gset

VACUUM (ANALYZE) vacuum_bloat.events;

SELECT jsonb_build_object(
         'schema_version', 'pgworkbench.operation-result/v1',
         'artifact_type', 'pgworkbench.operation-result',
         'operation_id', 'maintenance/vacuum-bloat-manual',
         'variant', 'manual-vacuum-analyze',
         'primary_metric', jsonb_build_object(
           'name', 'vacuum_elapsed_ms',
           'unit', 'milliseconds',
           'direction', 'lower-is-better',
           'value', round(
             extract(epoch FROM (clock_timestamp() - :'benchmark_started_at'::timestamptz)) * 1000,
             3
           )
         ),
         'measurement', jsonb_build_object(
           'basis', 'postgres-server-clock',
           'scope', 'PostgreSQL server-clock interval bracketing VACUUM (ANALYZE); includes client/server protocol gaps between the bracketing SELECT commands and VACUUM; excludes dead-tuple churn.'
         )
       )::text;
