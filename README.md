# Telivoz SMS Gateway

A modern A2P SMS gateway replacing the old Kannel + PHP/Yii + Java system.

| Part | Technology | Folder |
|---|---|---|
| Messaging engine, API, legacy import | Go 1.24, PostgreSQL 16 | [`gateway/`](gateway) |
| Web portal | React 19, TypeScript, Tailwind CSS v4, TanStack, ECharts | [`apps/web/`](apps/web) |
| Deployment | Docker / systemd, Prometheus alerts, CI | [`deploy/`](deploy), [`.github/`](.github) |
| Temporary fixes for the old system | SQL, Kannel config, PHP patch | [`legacy-hotfixes/`](legacy-hotfixes) |
| Plans | Full plan and the 30-day plan | [`docs/`](docs) |

## What it fixes from the old system

| Old problem | New behaviour | Proven by |
|---|---|---|
| Messages stored and charged twice | Charge, message and queue entry are written in one transaction; a message can be charged only once (database constraint); idempotency keys for HTTP | `TestEndToEndDeliveryBillingAndDLR`, `TestClientAPIv1AndLegacy` |
| Bulk messages held for hours | Durable queue with one worker per vendor connection, windowed SMPP, token-bucket throttling; background campaigns | 300 TPS load test, campaign upload |
| DLRs lost on restart, late | Vendor message IDs stored durably; DLRs forwarded instantly, held for offline clients; hex/decimal ID mismatch handled | `TestDLRSurvivesGatewayRestart`, `TestHexVendorIDsStillMatchDLR` |
| Same message resent to a vendor 1000 times | A message the vendor did not answer is never resent; bounded failover | `TestSilentVendorIsNeverResent`, `TestFailoverToNextVendor` |
| Boxes crash without logs, restarts on every change | One process with structured logs, health and metrics; configuration reloads live | live reload on every API change |
| Extra internal HTTP hop limits TPS | Routing and billing run inside the engine | load test: 300 TPS sustained, 0 DLRs missing |

New features: HTTP API v1 with API keys, sender-ID routing, LCR, failover, content rules, bulk campaigns,
Alaris-style analytics with team hierarchy, live traffic monitor, route tester, audit log, dark mode.

## Quick start (development)

```bash
# PostgreSQL 16 running locally, then:
export DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway?sslmode=disable
export GATEWAY_SECRET=$(openssl rand -hex 32) WEB_DIR=../apps/web/dist

cd apps/web && pnpm install && pnpm build && cd ../..
cd gateway
go run ./cmd/gateway create-admin admin@example.com 'change-me-now'
go run ./cmd/gateway serve          # portal on :8080, SMPP on :2775
```

Portal development with hot reload: `cd apps/web && pnpm dev` (proxies `/api` to `:8080`).

Tests (integration tests need a PostgreSQL database they may wipe):

```bash
cd gateway && TEST_DATABASE_URL=postgres://gateway:gateway@localhost:5432/gateway_test?sslmode=disable go test -p 1 ./...
```

Deployment, legacy data import and cutover: [`deploy/DEPLOY.md`](deploy/DEPLOY.md).

## APIs

- **SMPP 3.4** on `SMPP_ADDR` (transceiver, transmitter, receiver; IP allowlists; per-account TPS).
- **HTTP API v1** (`Authorization: Bearer <api key>`):
  - `POST /api/v1/messages`: `{"to","from","text","client_ref"}` or `{"messages":[…]}` (up to 1000). An `Idempotency-Key` header makes retries safe.
  - `GET /api/v1/messages/{id}`: status and DLR.
  - `GET /api/v1/balance`.
  - DLR webhooks go to the client's configured URL.
- **Legacy HTTP API** (`/api/legacy`): the old parameters (`username, password, to, from, message, messageid, action=balance`) and response format, so existing HTTP clients only change the URL.
