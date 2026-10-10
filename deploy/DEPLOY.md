# Installing the new gateway on the main VPS

This guide installs the new gateway **next to** the current Kannel system on the same server. It imports all
existing data, runs a parallel test, then moves traffic over with a rollback path at every step.

Nothing in steps 1–5 touches live traffic.

---

## 1. Prerequisites

- Ubuntu 22.04/24.04 (or similar) on the main VPS, with root access.
- **PostgreSQL 16** for the new system (the old MySQL/MariaDB stays as it is):

  ```bash
  sudo apt install -y postgresql-16
  sudo -u postgres psql -c "CREATE USER gateway PASSWORD 'CHANGE_ME';"
  sudo -u postgres psql -c "CREATE DATABASE gateway OWNER gateway;"
  ```

- Recommended PostgreSQL settings for a 64 GB server shared with MySQL (`/etc/postgresql/16/main/conf.d/gateway.conf`):

  ```
  shared_buffers = 8GB
  effective_cache_size = 24GB
  work_mem = 32MB
  max_connections = 200
  wal_compression = on
  checkpoint_timeout = 15min
  ```

## 2. Build and install

Either use Docker (`deploy/docker-compose.yml`), or build the binaries. Building needs Go 1.24+ and Node 22 with
pnpm (on any machine; the result is one static binary plus the `web` folder).

```bash
cd apps/web && pnpm install --frozen-lockfile && pnpm build && cd ../..
cd gateway && CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway && CGO_ENABLED=0 go build -o bin/smppsim ./cmd/smppsim && cd ..

sudo useradd --system --home /opt/telivoz telivoz
sudo mkdir -p /opt/telivoz/bin /opt/telivoz/web /etc/telivoz
sudo cp gateway/bin/gateway gateway/bin/smppsim /opt/telivoz/bin/
sudo cp -r apps/web/dist/* /opt/telivoz/web/
sudo cp deploy/gateway.env.example /etc/telivoz/gateway.env
sudo chmod 600 /etc/telivoz/gateway.env && sudo chown telivoz /etc/telivoz/gateway.env
sudo cp deploy/systemd/telivoz-gateway.service /etc/systemd/system/
```

Edit `/etc/telivoz/gateway.env`:

- `GATEWAY_SECRET`: generate with `openssl rand -hex 32` and store a copy in your password manager.
- `DATABASE_URL`: the PostgreSQL password from step 1.
- During the parallel run, use ports the old system does not use: `SMPP_ADDR=:2775`, `HTTP_ADDR=:8080`.

## 3. Create the database and the first admin

```bash
set -a; . /etc/telivoz/gateway.env; set +a
/opt/telivoz/bin/gateway migrate
/opt/telivoz/bin/gateway create-admin you@company.com 'a-long-password'
```

## 4. Import the existing data

Create a **read-only** MySQL user for the import (on the old database):

```sql
CREATE USER 'importer'@'localhost' IDENTIFIED BY 'CHANGE_ME';
GRANT SELECT ON <old_database>.* TO 'importer'@'localhost';
```

Run the import (safe to repeat; it never creates duplicates):

```bash
/opt/telivoz/bin/gateway import-legacy 'importer:CHANGE_ME@tcp(127.0.0.1:3306)/<old_database>'
```

It prints a report. What gets imported:

| Old system | New system |
|---|---|
| countries, networks, network_prefixes | countries, networks, number prefixes (number lookup) |
| users | users, **same passwords** (staff keep their login; client users stay client logins) |
| clients, sms_credits_balances, company_settings | clients with balances; DLR webhook URL kept in the **old format** |
| smpp_users, api_credentials | SMPP and HTTP accounts, **same usernames and passwords** |
| vendors, sms_gateways (SMPP) | vendors and connections, **imported disabled** so they do not bind twice |
| client_rates, vendor_rates | client and vendor rates |
| routes, routing_distributed_data | routes (priority, round robin, distributed, LCR, sender rules) |
| outgoing_sms + archives | message history (search and reports) |
| mt_reports | daily statistics (dashboards and analytics history) |

Check the report: clients and accounts must match the old portal, and balances must match.

## 5. Parallel test (no live traffic)

```bash
sudo systemctl daemon-reload && sudo systemctl enable --now telivoz-gateway
curl -s localhost:8080/healthz      # ok
```

Open the portal on port 8080 (put it behind HTTPS with nginx or Caddy) and sign in.

1. **Check the data**: clients, balances, accounts, rates, routes.
2. **Route tester**: try real numbers for your main clients and check the vendor and price.
3. **Vendor test with a simulator** (no real vendor needed):

   ```bash
   /opt/telivoz/bin/smppsim vendor -listen 127.0.0.1:9000
   ```

   Then add a test connection to `127.0.0.1:9000`, a route, an SMPP test account, and run:

   ```bash
   /opt/telivoz/bin/smppsim client -addr 127.0.0.1:2775 -user TEST -pass TEST -n 6000 -tps 300
   ```

   Expected: 6000 accepted, all DLRs returned, the client balance reduced exactly once per message.
4. **Real vendor test**: if a vendor allows an extra bind, enable that one connection with `binds = 1` and send a
   few messages to staff phones through a test account.

## 6. Cutover (low-traffic window)

Old opensmppbox listens on the port your clients use (for example 4775).

1. **Freeze**: re-run the import so balances and settings are current:
   `gateway import-legacy …` (repeatable).
2. **Stop the old client-facing side**: stop opensmppbox and the PHP API so clients cannot submit to the old system.
   Let Kannel finish what it has queued (watch its status page until the queues are empty), then stop it.
3. **Enable the vendor connections** in the new portal (Connections → edit → status *Enabled*). Check Live traffic:
   every bind should turn green.
4. **Switch the client port**: set `SMPP_ADDR=:4775` (the old port) in `/etc/telivoz/gateway.env` and
   `sudo systemctl restart telivoz-gateway`. Clients reconnect automatically, with the same credentials.
5. **HTTP clients**: point the old HTTP API URL at the new `/api/legacy` endpoint (same parameters and responses),
   e.g. with an nginx `location` rewrite. New integrations use `/api/v1/messages`.
6. **Watch** Live traffic and Analytics for an hour: TPS, delivery rate, binds, queue.

## 7. Rollback (if needed)

1. Disable the vendor connections in the new portal (or stop the service).
2. Set `SMPP_ADDR` back to `:2775` and restart the new gateway.
3. Start Kannel and opensmppbox again; clients reconnect to the old system.

Messages already accepted by the new gateway stay in its database (searchable, billed once).

## 8. Day-to-day operations

- **Logs**: `journalctl -u telivoz-gateway -f` (JSON lines; every message has its ID in the logs).
- **Health**: `GET /healthz`. **Metrics**: `GET /metrics` (Prometheus); alert rules in `deploy/monitoring/alerts.yml`.
  Grafana: *Dashboards → Import* `deploy/monitoring/grafana-dashboard.json` and pick the Prometheus data source.
- **Backups**: `pg_dump -Fc gateway > gateway-$(date +%F).dump` daily, plus WAL archiving for point-in-time
  recovery. Test a restore once a month.
- **Config changes** (routes, rates, vendors, accounts) apply immediately; no restart is ever needed.
- **Upgrades**: replace the binary and `web` folder, then `systemctl restart telivoz-gateway`. Migrations run
  automatically at start.

## 9. After go-live: recommended settings

- **Two-factor sign-in**: every admin, manager and finance user turns it on under *Settings → Two-factor sign-in*.
  If a phone is lost, an admin uses *Users → Reset 2FA*. This also signs the user out everywhere.
- **Blacklist**: import any existing do-not-contact lists under *Blacklist → Import list*, as one number per line or CSV.
  Clients can manage their own list, and staff can add global entries.
- **Incoming SMS**: create *Incoming routes* (number prefix and/or keyword → client) and set each client's MO webhook,
  or let the client bind SMPP as receiver/transceiver. STOP-type replies add the sender to that client's blacklist.
- **DLR failover**: on each vendor connection, list the DLR results that should try the next vendor, for example
  `UNDELIV:011` or `REJECTD`. Only list codes that mean "this route cannot deliver", never generic failures.
- **Excel**: rate imports, bulk campaign uploads and blacklist imports accept `.xlsx` as well as CSV. Reports and
  message/DLR searches download as Excel or CSV (up to 100,000 messages per download).
- **API docs**: clients find the HTTP API, webhook formats and SMPP details under *API docs* in the portal.
- **Statements**: *Statements* shows each client's monthly usage, payments and balances. Use *Print / PDF* to send it.
- **Mobile app**: staff and clients sign in with the portal address and their usual login. See `apps/mobile/README.md` for store builds.
- **Docker image**: the final image downloads nothing at build time (it uses the Alpine CA bundle and embedded
  time zones), and runs as a non-root user. Set `TZ` if logs should use local time.
