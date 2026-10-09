# Telivoz SMS Gateway

A modern A2P SMS gateway replacing the old Kannel + PHP/Yii + Java system.

| Part | Technology | Folder |
|---|---|---|
| Messaging engine, API, legacy import | Go 1.24, PostgreSQL 16 | [`gateway/`](gateway) |
| Web portal | React 19, TypeScript, Tailwind CSS v4, TanStack, ECharts | [`apps/web/`](apps/web) |
| Mobile app (Android / iOS) | React Native, Expo, TypeScript, NativeWind (Tailwind) | [`apps/mobile/`](apps/mobile) |
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
| Vendor accepts but never delivers (bad route) | Per-connection list of DLR codes (e.g. `UNDELIV:011`) that reroute the message to the next vendor, without charging the client again | `TestFailoverAfterNegativeDLR` |
| Boxes crash without logs, restarts on every change | One process with structured logs, health and metrics; configuration reloads live | live reload on every API change |
| Extra internal HTTP hop limits TPS | Routing and billing run inside the engine | load test: 300 TPS sustained, 0 DLRs missing |

New features:

- Routing and delivery: sender-ID routing, LCR and weighted routes, failover on reject and on negative DLRs, content rules, bulk campaigns.
- Clients: HTTP API v1 with API keys.
  - **Blacklists** (global or per client, CSV import; STOP replies are added automatically).
  - **Incoming SMS (MO)** forwarded to clients over SMPP or webhook.
  - **Monthly statements** (print or save as PDF).
- Insight: Alaris-style analytics with team hierarchy, live traffic monitor, route tester.
- Security and operations: audit log, **two-factor sign-in (TOTP)**, dark mode.
- **Mobile app**: dashboard, live traffic, vendor connections and message search.

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
Mobile app: `cd apps/mobile && npm ci && npm start` (see [`apps/mobile/README.md`](apps/mobile/README.md)).

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
  - DLR webhooks go to the client's configured URL; incoming SMS go to the client's MO webhook as
    `{"type":"mo","id","from","to","text","received_at"}`.
- **Legacy HTTP API** (`/api/legacy`): the old parameters (`username, password, to, from, message, messageid, action=balance`) and response format, so existing HTTP clients only change the URL.
