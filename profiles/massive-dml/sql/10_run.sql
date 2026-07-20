\set ON_ERROR_STOP on

\echo 'massive-dml profile: representative UPDATE batch inside rollback'
\ir 50_explain_one_update_batch.sql
