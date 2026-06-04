SELECT
    current_database() AS database,
    current_setting('server_version') AS server_version,
    current_setting('server_version_num') AS server_version_num;

SELECT
    schemaname,
    count(*) AS tables
FROM pg_tables
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
GROUP BY schemaname
ORDER BY schemaname;
