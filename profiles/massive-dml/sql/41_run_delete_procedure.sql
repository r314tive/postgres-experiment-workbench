\set ON_ERROR_STOP on

\ir 40_delete_procedure.sql

SELECT CASE :'profile_size'
    WHEN 'small' THEN 500
    WHEN 'medium' THEN 5000
    WHEN 'large' THEN 10000
    ELSE 500
END AS massive_dml_batch_size
\gset

CALL massive_dml.delete_old_audit_records(
    TIMESTAMPTZ '2026-04-07 00:00:00+00',
    :massive_dml_batch_size,
    0
);
