DROP PUBLICATION IF EXISTS :"publication_name";
CREATE PUBLICATION :"publication_name" FOR TABLE logical_repl.events;

SELECT
    pubname,
    puballtables,
    pubinsert,
    pubupdate,
    pubdelete
FROM pg_publication
WHERE pubname = :'publication_name';
