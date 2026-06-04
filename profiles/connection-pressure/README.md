# connection-pressure Profile

Generic profile for session churn and pooler-shaped behavior.

It is useful directly against PostgreSQL and through `TOPOLOGY=pgbouncer`.
The profile keeps data small by default and focuses on observable connection
state, backend reuse, and bounded write/read activity.
