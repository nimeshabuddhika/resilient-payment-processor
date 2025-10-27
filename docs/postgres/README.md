# HA Postgres (Primary + Read Replicas) with PgBouncer + HAProxy

**Status:** Demo / reference architecture only.  
**Goal:** Demonstrate a high-availability–style Postgres topology that can serve **10k+ concurrent jobs** by **pooling connections** and **spreading reads** across replicas.  
**Non-Goals:** This setup is **not security-hardened** and **does not implement automatic failover**. Production hardening (TLS, secrets management, auth/Z, backups, failover automation) is a **future task**.

---

## Topology

Docker Compose brings up:

- **postgres-primary** – PostgreSQL 17.5 **write leader**. Initializes DB, enables streaming replication.
- **postgres-replica-1 / postgres-replica-2** – **hot standbys** via `pg_basebackup` + physical replication slots (read-only).
- **haproxy-read** – Layer-4 TCP load balancer for **read traffic** across replicas.
- **pgbouncer-write** – Connection **pooler** (transaction mode) in front of the **primary** for all writes.
- **pgbouncer-read** – Connection **pooler** (transaction mode) in front of **haproxy-read** for all reads.

> Why this configuration? PgBouncer keeps Postgres from being overwhelmed by client connections, and HAProxy spreads read load. We keep read/write endpoints separate to avoid SQL parsing in the proxy.

---

## Addresses & Ports

| Purpose                   | Container DNS (inside Compose network) | Port (in-container) |        Host Port |
|---------------------------|----------------------------------------|--------------------:|-----------------:|
| Primary (writes via pool) | `pgbouncer-write`                      |                6432 | `localhost:6432` |
| Read LB (reads via pool)  | `pgbouncer-read`                       |                6432 | `localhost:6433` |
| HAProxy (replicas, L4)    | `haproxy-read`                         |                5432 | `localhost:5440` |
| Primary (direct)          | `postgres-primary`                     |                5432 | `localhost:5432` |

**In-container DSNs (what your services should use):**
- **WRITE_DSN**: `db_user:db_password@pgbouncer-write:6432/resilient_payment_processor?sslmode=disable`
- **READ_DSN**:  `db_user:db_password@pgbouncer-read:6432/resilient_payment_processor?sslmode=disable`

**From your host** (psql/testing), use `localhost` with host ports 6432/6433.

---

## Files & Layout

```filetree
/configs/postgres/
├─ primary/init/ # init SQL & shell for primary
├─ replica/entrypoint.sh # bootstraps standbys via pg_basebackup
├─ haproxy/haproxy.cfg # L4 LB over replicas
├─ pgbouncer/
    ├─ pgbouncer-write.ini # pool → primary
    ├─ pgbouncer-read.ini # pool → HAProxy (replicas)
    ├─ userlist.txt # "pgbouncer" + "db_user" creds
```

## Container Roles (what each does)

### postgres-primary (postgres:17.5)
- Creates `db_user` and the target database and schema.
- Enables streaming replication (`wal_level=replica`, `max_wal_senders`, `max_replication_slots`, `hot_standby=on`, `wal_keep_size`).
- Accepts replication from `repl_user` (see primary init SQL/hba).
- **Writes** land here. **Strongest consistency**.

### postgres-replica-1 / postgres-replica-2 (postgres:17.5)
- On first start, run `pg_basebackup -R -C -S <slot>` from the primary:
    - Creates `standby.signal`, sets `primary_conninfo`, and registers a **physical replication slot** to avoid WAL loss.
- Serve **read-only** queries with minimal lag (depends on workload/network).

### haproxy-read (haproxy:3.2)
- TCP load balances **only read traffic** across the two replicas (round-robin).
- Can use `option pgsql-check` or `tcp-check` to actively verify backend health.
- Gives you a single **read endpoint** even if one replica is down.

### pgbouncer-write / pgbouncer-read (edoburu/pgbouncer:v1.24.1-p1)
- **Transaction pooling** to multiplex thousands of client connections into a sane number of server sessions.
- `pgbouncer-write` routes to the **primary**.
- `pgbouncer-read` routes to **haproxy-read** (which fans out to replicas).
- Suggested knobs (tune per host): `max_client_conn`, `default_pool_size`.

---