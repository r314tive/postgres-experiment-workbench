\set ON_ERROR_STOP on

\ir 20_procedure_update.sql

SELECT CASE :'profile_size'
    WHEN 'small' THEN 500
    WHEN 'medium' THEN 5000
    WHEN 'large' THEN 10000
    ELSE 500
END AS massive_dml_batch_size
\gset

CALL massive_dml.backfill_transaction_log_timestamps(:massive_dml_batch_size, 0);
