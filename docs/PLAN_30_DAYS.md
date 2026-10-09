# Telivoz SMS Gateway — 30-Day Delivery Plan (Plan 2, essential scope)

**Status:** Draft v1 for review · **Date:** 9 October 2026

The client chose **Plan 2 (the new platform)** and needs it live within **30 days**. This document cuts the full plan in [`MODERNIZATION_PLAN.md`](MODERNIZATION_PLAN.md) down to what is needed to replace the current system and fix the reported issues. Everything else moves to a follow-up phase after go-live.

> **Honest note:** 30 days is tight for a new gateway. It is achievable only with the reduced scope below, the team in §4, a scope freeze on day 3, and the client inputs in §5 arriving by day 2. If any of these slip, the cut list in §3.2 says what moves out first.

---

## 1. What "done" means on day 30

- All client and vendor traffic runs on the new platform, with the old Kannel system kept switched on as a fallback.
- The six reported issues are fixed, and tests prove it:

| Reported issue | Proven by |
|---|---|
| Double messages and double billing | Zero duplicate charges in load and crash tests. Each message ID can only be charged once (database constraint). |
| Bulk messages delayed | A 100k-number upload starts sending within seconds, at the account's TPS. |
| DLRs lost or late | No DLRs lost when the engine restarts. DLRs forwarded in under 1 second (p95). |
| Same message resent to a vendor | A vendor that never answers gets at most the configured number of attempts. |
| Boxes crash with no logs | Every error is logged with the message ID and triggers an alert. Settings changes need no restart. |
| Internal HTTP route limits TPS | Removed. Routing and billing run inside the engine. 300 TPS sustained for 1 hour in the load test. |

- The client's requested features work: HTTP API, sender-ID routing, LCR, failover, content changes, fast bulk upload, reports with team hierarchy.
- The new GUI is live: modern React + TypeScript + Tailwind portal for admins and clients.

---

## 2. Simplified technology (to save time)

Same languages as the full plan, fewer moving parts:

| Part | Full plan | 30-day version | Why |
|---|---|---|---|
| Engine and API | Go | **Go**, one program (engine + portal API in one binary) | One thing to build, deploy and monitor. |
| Queue | NATS JetStream | **PostgreSQL** as the queue (`SKIP LOCKED` + `LISTEN/NOTIFY`) | 300 TPS is easy for PostgreSQL. One less system to run. NATS can be added later if traffic grows a lot. |
| Database | PostgreSQL | **PostgreSQL** | Same. |
| Cache and rate limits | Redis | **Redis** | Same. |
| Reports | ClickHouse | **PostgreSQL** hourly and daily summary tables, updated by the engine | Fast enough for 30-day volumes. ClickHouse comes in the follow-up phase for Alaris-level analytics. |
| Web GUI | React + TypeScript + Tailwind | **React + TypeScript + Tailwind + shadcn/ui** | Same. Uses ready-made shadcn dashboard blocks styled with the client's colors, instead of a full Figma design phase. |
| Mobile app | React Native | **After go-live** | Not needed to replace the current system. |
| Monitoring | Prometheus, Grafana, Loki | **Prometheus + Grafana** with alerts | Logs go to files/journald. Loki comes later. |

---

## 3. Scope

### 3.1 In the 30 days (must have)

**Messaging engine (Go)**
- SMPP server for clients: login with username, password and IP allowlist; per-account TPS limit; long messages; GSM and Unicode.
- SMPP connections to vendors: multiple binds per vendor, auto-reconnect, per-vendor TPS, bounded retries (fixes the "1000 resends" issue).
- Message saved before the client gets its acknowledgement, so nothing is lost or duplicated on a crash.
- **Routing:** priority, percentage split, **LCR** (cheapest vendor rate per country/network, with a margin check), **failover** to the next vendor on errors, **sender ID rules**.
- **Content rules:** find-and-replace (including regex) and sender rewrite, with conditions on client, sender and destination.
- **Billing:** prepaid balance and postpaid credit limit, stored in a ledger; a message can never be charged twice.
- **DLRs:** stored in the database, forwarded instantly to SMPP clients (held until they connect) or by webhook to HTTP clients.
- MO (incoming) messages forwarded to the client.
- Route, rate and vendor changes apply live, with no restart.

**HTTP API for clients**
- Send single or batch, check status, check balance, DLR webhooks, API keys, IP allowlist, duplicate protection (`Idempotency-Key`), and an API docs page.

**Web portal (React + TypeScript + Tailwind)**
- **Admin:**
  - Live dashboard: TPS, queue size, delivery rate, connection status.
  - Clients and accounts (SMPP and HTTP).
  - Vendors and connections, with start/stop per connection.
  - Rate import from CSV/XLSX for clients and vendors.
  - Routing rules with a "test a number" check.
  - Content rules and sender IDs.
  - Balances and top-ups.
  - Users, roles and **team hierarchy** (manager → team lead → sales; each person sees only their own and their team's clients and vendors).
  - Message search (CDR).
  - **Reports:** traffic, delivery rate, revenue, cost and margin by client, vendor, country, network and sender, by hour or day, with CSV/XLSX export.
- **Client:**
  - Dashboard and balance.
  - Single send and **bulk upload** (large CSV/XLSX files, processed in the background with a progress bar).
  - Reports and DLR export.
  - API keys.
- Dark and light mode, works on phones, 2FA for admins.

**Migration**
- Import clients, accounts, vendors, connections, rates, routes, sender IDs, balances, users and the MCC/MNC prefix table from the old database, with a check report.
- Cut over clients in groups, with the old system ready as a fallback.

**Quick fixes to the old system (first 3 days only)**
The old system carries traffic for ~4 more weeks, so only the highest-value, lowest-risk fixes:
- Store Kannel DLRs in the database instead of memory.
- Cap vendor resends (`sms-resend-retry`, `wait-ack-expire`).
- Stop full Kannel restarts on routing changes.
- Add the unique key that blocks double charging.

### 3.2 After go-live (follow-up phase, days 31–60)

Listed in the order they would be cut first if the 30 days come under pressure (top = cut first):

1. Mobile app (React Native).
2. Alaris-level analytics on ClickHouse: pivot tables, drill-down, scheduled email reports.
3. Invoices (PDF) and online payment gateways.
4. Vendor quality scoring and quality-based routing.
5. Vendor connectors over HTTP (only needed if some vendors don't use SMPP; confirm on day 2).
6. HLR/MNP number lookup.
7. White-label branding per reseller, passkeys.
8. Old portal modules (surveys, voting, birthday SMS, phonebook, support tickets) if the client still uses them.
9. Second server for high availability.

---

## 4. Team and timeline

**Team needed (full-time for 30 days):**
- **Backend developer 1 (Go):** SMPP server, vendor connections, routing, DLRs.
- **Backend developer 2 (Go):** billing, HTTP API, portal API, reports, data import.
- **Frontend developer (React + TypeScript):** the whole web portal.
- **QA/DevOps (part-time):** servers, monitoring, load tests, migration runs.

With fewer people, the cut list in §3.2 starts earlier.

### Week-by-week plan

| Days | Backend 1 (engine) | Backend 2 (billing, API, data) | Frontend (portal) | Done when |
|---|---|---|---|---|
| **1–3** | Quick fixes on the old system. Project setup, server stack (PostgreSQL, Redis, Grafana), SMPP test simulators. | Database schema. Start the data import from the old database. Login, roles and hierarchy. | Portal setup: layout, login, theme in client colors, dark mode. | Old system stable. **Scope frozen.** Inputs from §5 received. |
| **4–10** | SMPP server for clients. SMPP connections to vendors. Save-before-acknowledge. Retry limits. | Billing ledger (prepaid/postpaid). Rate import. Data import finished with a check report. | Clients, accounts, vendors, connections, rates screens. | A test message goes client → engine → vendor simulator and is billed once. |
| **11–17** | Routing (priority, split, LCR, failover, sender ID). Content rules. DLR matching and forwarding. MO. Live config reload. | HTTP API, webhooks and API docs. Summary tables for reports. Message search. | Routing rules and "test a number". Content rules. Live dashboard. Message search. | Full flow works with DLRs back to the client. Routing decisions match the old system on sample numbers. |
| **18–23** | Load tests (300 TPS for 1 hour, 1,000 TPS burst). Crash tests. Fix what they find. | Bulk upload processing in the background. Reports by hierarchy. Alerts. | Client portal: dashboard, single send, bulk upload with progress, reports, API keys. Admin reports. | Load and crash tests pass. Client tests the portal (UAT starts). |
| **24–27** | Pilot: internal test accounts, then 2–3 friendly clients and one vendor at a time. | Final data import. Balance freeze and transfer for pilot clients. | UAT fixes. | Pilot clients run cleanly for 48 hours, with billing matching. |
| **28–30** | Move remaining clients in groups. Monitor. | Balance transfers. Daily billing check. | Final fixes. | All traffic on the new platform. Old system kept ready as a fallback. |

---

## 5. Needed from the client by day 2

1. Live Kannel configs and the missing components (`MessageDispatcherL`, whatever writes `delivery_reports`).
2. Read-only access to the production database (or a fresh dump).
3. Numbers: clients, accounts, vendors, binds, average and peak TPS.
4. Billing rules: charge on submit or on delivery? Refund on failure? Currencies?
5. Which vendor error codes should trigger failover.
6. Do any vendors connect over HTTP instead of SMPP?
7. Logo and colors for the portal.
8. Can clients keep the same SMPP IP and port at cutover, or will they change settings?
9. Two or three friendly clients willing to pilot in week 4.
10. One decision-maker who can approve scope and UAT within a day.

---

## 6. Main risks

| Risk | What we do |
|---|---|
| Not finished in 30 days | Scope frozen on day 3. Cut list (§3.2) applied in order. Pilot clients go first, so the deadline can be met for most traffic even if a few clients move later. |
| A client's or vendor's SMPP setup behaves differently | Test each connection in the pilot. Old system kept as a fallback for that client. |
| Wrong balances or rates after import | Check report after every import. Balances frozen and confirmed with the client before each group moves. |
| Inputs arrive late | Each day of delay moves go-live by about a day, or moves items to the follow-up phase. |
