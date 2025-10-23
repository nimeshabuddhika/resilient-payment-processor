-- app user already created by env (db_user/db_password)

-- replication user
CREATE ROLE repl_user WITH REPLICATION LOGIN PASSWORD 'repl_password';

-- allow hot standby & streaming replication
ALTER SYSTEM SET listen_addresses = '*';
ALTER SYSTEM SET wal_level = 'replica';
ALTER SYSTEM SET max_wal_senders = 10;
ALTER SYSTEM SET max_replication_slots = 10;
ALTER SYSTEM SET hot_standby = 'on';
ALTER SYSTEM SET wal_keep_size = '1024MB';
-- OPTIONAL: stronger consistency, slower writes
-- ALTER SYSTEM SET synchronous_standby_names = 'ANY 1 (replica1, replica2)';
SELECT pg_reload_conf();

-- PgBouncer auth user for auth_query or auth_file
CREATE ROLE pgbouncer LOGIN PASSWORD 'pgbouncer_password';
