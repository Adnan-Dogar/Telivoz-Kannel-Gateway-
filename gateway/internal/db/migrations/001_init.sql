-- Telivoz gateway schema v1 (PostgreSQL 16+).
-- Money is NUMERIC(18,6). legacy_id columns let the legacy import run repeatedly without creating duplicates.

-- ---------------------------------------------------------------------------------------------------------
-- Reference data
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE countries (
    iso        CHAR(2) PRIMARY KEY,
    name       TEXT NOT NULL,
    dial_code  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE networks (
    id          BIGSERIAL PRIMARY KEY,
    country_iso CHAR(2) REFERENCES countries (iso),
    mcc         TEXT NOT NULL,
    mnc         TEXT NOT NULL,
    name        TEXT NOT NULL DEFAULT '',
    UNIQUE (mcc, mnc)
);

-- Longest-prefix match on the destination number gives the network.
CREATE TABLE number_prefixes (
    prefix     TEXT PRIMARY KEY,
    network_id BIGINT NOT NULL REFERENCES networks (id) ON DELETE CASCADE
);

-- ---------------------------------------------------------------------------------------------------------
-- People and access
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    password_hash TEXT NOT NULL DEFAULT '',
    -- admin: everything. manager/team_lead/sales: own + team's clients and vendors. client: own client only.
    role          TEXT NOT NULL CHECK (role IN ('admin', 'manager', 'team_lead', 'sales', 'finance', 'noc', 'client')),
    manager_id    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    client_id     BIGINT,
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    last_login_at TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id     BIGINT UNIQUE
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    ip         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX sessions_user_idx ON sessions (user_id);

CREATE TABLE audit_log (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    action     TEXT NOT NULL,
    entity     TEXT NOT NULL,
    entity_id  TEXT NOT NULL DEFAULT '',
    details    JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_idx ON audit_log (created_at DESC);

-- ---------------------------------------------------------------------------------------------------------
-- Clients, accounts, billing
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE clients (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT NOT NULL,
    email           TEXT NOT NULL DEFAULT '',
    phone           TEXT NOT NULL DEFAULT '',
    country_iso     CHAR(2),
    currency        CHAR(3) NOT NULL DEFAULT 'USD',
    billing_type    TEXT NOT NULL DEFAULT 'prepaid' CHECK (billing_type IN ('prepaid', 'postpaid')),
    credit_limit    NUMERIC(18, 6) NOT NULL DEFAULT 0,
    owner_id        BIGINT REFERENCES users (id) ON DELETE SET NULL,
    parent_id       BIGINT REFERENCES clients (id) ON DELETE SET NULL,
    dlr_webhook_url TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id       BIGINT UNIQUE
);
CREATE INDEX clients_owner_idx ON clients (owner_id);

ALTER TABLE users ADD CONSTRAINT users_client_fk FOREIGN KEY (client_id) REFERENCES clients (id) ON DELETE CASCADE;

-- An SMPP bind login or an HTTP API login belonging to a client.
CREATE TABLE accounts (
    id            BIGSERIAL PRIMARY KEY,
    client_id     BIGINT NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('smpp', 'http')),
    username      TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    allowed_ips   TEXT[] NOT NULL DEFAULT '{}',
    tps           INT NOT NULL DEFAULT 50 CHECK (tps > 0),
    max_binds     INT NOT NULL DEFAULT 4 CHECK (max_binds > 0),
    status        TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id     TEXT UNIQUE,
    UNIQUE (kind, username)
);
CREATE INDEX accounts_client_idx ON accounts (client_id);

CREATE TABLE api_keys (
    id           BIGSERIAL PRIMARY KEY,
    account_id   BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    prefix       TEXT NOT NULL,
    key_hash     BYTEA NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE TABLE balances (
    client_id  BIGINT PRIMARY KEY REFERENCES clients (id) ON DELETE CASCADE,
    balance    NUMERIC(18, 6) NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Every balance change. A message can be charged and refunded at most once each.
CREATE TABLE ledger (
    id            BIGSERIAL PRIMARY KEY,
    client_id     BIGINT NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    message_id    UUID,
    kind          TEXT NOT NULL CHECK (kind IN ('charge', 'refund', 'topup', 'adjustment', 'migration')),
    amount        NUMERIC(18, 6) NOT NULL,
    balance_after NUMERIC(18, 6) NOT NULL,
    note          TEXT NOT NULL DEFAULT '',
    created_by    BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX ledger_message_kind_key ON ledger (message_id, kind) WHERE message_id IS NOT NULL;
CREATE INDEX ledger_client_idx ON ledger (client_id, created_at DESC);

-- ---------------------------------------------------------------------------------------------------------
-- Vendors and connections
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE vendors (
    id         BIGSERIAL PRIMARY KEY,
    name       TEXT NOT NULL,
    email      TEXT NOT NULL DEFAULT '',
    currency   CHAR(3) NOT NULL DEFAULT 'USD',
    owner_id   BIGINT REFERENCES users (id) ON DELETE SET NULL,
    status     TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id  BIGINT UNIQUE
);

-- One SMPP connection to a vendor (with one or more binds).
CREATE TABLE connections (
    id               BIGSERIAL PRIMARY KEY,
    vendor_id        BIGINT NOT NULL REFERENCES vendors (id) ON DELETE CASCADE,
    name             TEXT NOT NULL UNIQUE,
    host             TEXT NOT NULL,
    port             INT NOT NULL,
    system_id        TEXT NOT NULL,
    password_enc     TEXT NOT NULL DEFAULT '',
    system_type      TEXT NOT NULL DEFAULT '',
    bind_mode        TEXT NOT NULL DEFAULT 'trx' CHECK (bind_mode IN ('trx', 'tx', 'rx')),
    binds            INT NOT NULL DEFAULT 1 CHECK (binds BETWEEN 1 AND 32),
    tps              INT NOT NULL DEFAULT 50 CHECK (tps > 0),
    window_size      INT NOT NULL DEFAULT 10 CHECK (window_size > 0),
    source_ton       SMALLINT NOT NULL DEFAULT 5,
    source_npi       SMALLINT NOT NULL DEFAULT 0,
    dest_ton         SMALLINT NOT NULL DEFAULT 1,
    dest_npi         SMALLINT NOT NULL DEFAULT 1,
    -- how a vendor message ID in a DLR relates to the one in submit_sm_resp
    dlr_id_format    TEXT NOT NULL DEFAULT 'auto' CHECK (dlr_id_format IN ('auto', 'same', 'hex_to_dec', 'dec_to_hex')),
    max_attempts     INT NOT NULL DEFAULT 2 CHECK (max_attempts BETWEEN 1 AND 5),
    status           TEXT NOT NULL DEFAULT 'enabled' CHECK (status IN ('enabled', 'disabled')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id        BIGINT UNIQUE
);

-- ---------------------------------------------------------------------------------------------------------
-- Rates (network-specific rate wins over the country rate; newest effective rate wins)
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE client_rates (
    id             BIGSERIAL PRIMARY KEY,
    client_id      BIGINT NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    country_iso    CHAR(2) NOT NULL,
    network_id     BIGINT REFERENCES networks (id) ON DELETE CASCADE,
    price          NUMERIC(18, 6) NOT NULL CHECK (price >= 0),
    effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id      BIGINT UNIQUE
);
CREATE UNIQUE INDEX client_rates_key ON client_rates (client_id, country_iso, COALESCE(network_id, 0), effective_from);

CREATE TABLE vendor_rates (
    id             BIGSERIAL PRIMARY KEY,
    connection_id  BIGINT NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    country_iso    CHAR(2) NOT NULL,
    network_id     BIGINT REFERENCES networks (id) ON DELETE CASCADE,
    price          NUMERIC(18, 6) NOT NULL CHECK (price >= 0),
    effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id      BIGINT UNIQUE
);
CREATE UNIQUE INDEX vendor_rates_key ON vendor_rates (connection_id, country_iso, COALESCE(network_id, 0), effective_from);

-- ---------------------------------------------------------------------------------------------------------
-- Routing and content rules
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE routes (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    priority       INT NOT NULL DEFAULT 100,
    client_id      BIGINT REFERENCES clients (id) ON DELETE CASCADE,
    account_id     BIGINT REFERENCES accounts (id) ON DELETE CASCADE,
    country_iso    CHAR(2),
    network_id     BIGINT REFERENCES networks (id) ON DELETE CASCADE,
    sender_match   TEXT NOT NULL DEFAULT 'any' CHECK (sender_match IN ('any', 'exact', 'prefix', 'regex')),
    sender_pattern TEXT NOT NULL DEFAULT '',
    -- priority: targets in order (next one on failure). weighted: split by weight. lcr: cheapest vendor rate first.
    policy         TEXT NOT NULL DEFAULT 'priority' CHECK (policy IN ('priority', 'weighted', 'lcr')),
    allow_loss     BOOLEAN NOT NULL DEFAULT false,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    legacy_id      BIGINT UNIQUE
);

CREATE TABLE route_targets (
    route_id      BIGINT NOT NULL REFERENCES routes (id) ON DELETE CASCADE,
    connection_id BIGINT NOT NULL REFERENCES connections (id) ON DELETE CASCADE,
    position      INT NOT NULL DEFAULT 0,
    weight        INT NOT NULL DEFAULT 100 CHECK (weight >= 0),
    PRIMARY KEY (route_id, connection_id)
);

CREATE TABLE content_rules (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    priority       INT NOT NULL DEFAULT 100,
    client_id      BIGINT REFERENCES clients (id) ON DELETE CASCADE,
    country_iso    CHAR(2),
    sender_pattern TEXT NOT NULL DEFAULT '',
    text_pattern   TEXT NOT NULL DEFAULT '',
    action         TEXT NOT NULL CHECK (action IN ('replace_text', 'replace_sender', 'prepend', 'append', 'block')),
    find           TEXT NOT NULL DEFAULT '',
    replace_with   TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------------------------------------
-- Messages (monthly partitions; history imported from the legacy system lands here too)
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE messages (
    id              UUID NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    client_id       BIGINT NOT NULL,
    account_id      BIGINT,
    direction       TEXT NOT NULL DEFAULT 'mt' CHECK (direction IN ('mt', 'mo')),
    source          TEXT NOT NULL DEFAULT '',
    destination     TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    data_coding     SMALLINT NOT NULL DEFAULT 0,
    -- SMPP submits are forwarded byte-for-byte (payload + UDH) unless a content rule changed the text
    payload         BYTEA,
    udh             BYTEA,
    wants_dlr       BOOLEAN NOT NULL DEFAULT true,
    parts           SMALLINT NOT NULL DEFAULT 1,
    country_iso     CHAR(2),
    network_id      BIGINT,
    route_id        BIGINT,
    connection_id   BIGINT,
    -- queued, sent, delivered, undelivered, expired, rejected, failed, unknown
    status          TEXT NOT NULL DEFAULT 'queued',
    attempts        SMALLINT NOT NULL DEFAULT 0,
    route_plan      BIGINT[] NOT NULL DEFAULT '{}',
    price           NUMERIC(18, 6) NOT NULL DEFAULT 0,
    cost            NUMERIC(18, 6) NOT NULL DEFAULT 0,
    client_ref      TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    dlr_status      TEXT NOT NULL DEFAULT '',
    dlr_error       TEXT NOT NULL DEFAULT '',
    sent_at         TIMESTAMPTZ,
    dlr_at          TIMESTAMPTZ,
    dlr_sent_at     TIMESTAMPTZ,
    legacy_id       BIGINT,
    PRIMARY KEY (id, created_at)
) PARTITION BY RANGE (created_at);
CREATE INDEX messages_created_idx ON messages (created_at DESC);
CREATE INDEX messages_client_idx ON messages (client_id, created_at DESC);
CREATE INDEX messages_destination_idx ON messages (destination, created_at DESC);
CREATE INDEX messages_legacy_idx ON messages (legacy_id) WHERE legacy_id IS NOT NULL;
CREATE TABLE messages_default PARTITION OF messages DEFAULT;

-- Creates the monthly partition that holds `at` (idempotent).
CREATE FUNCTION ensure_messages_partition(at TIMESTAMPTZ) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    start_at TIMESTAMPTZ := date_trunc('month', at);
    part     TEXT := 'messages_' || to_char(start_at, 'YYYY_MM');
BEGIN
    IF to_regclass(part) IS NULL THEN
        EXECUTE format('CREATE TABLE %I PARTITION OF messages FOR VALUES FROM (%L) TO (%L)',
                       part, start_at, start_at + INTERVAL '1 month');
    END IF;
END $$;
SELECT ensure_messages_partition(now());
SELECT ensure_messages_partition(now() + INTERVAL '1 month');

-- HTTP Idempotency-Key / client reference: the same key from the same account creates one message.
CREATE TABLE idempotency_keys (
    account_id BIGINT NOT NULL,
    key        TEXT NOT NULL,
    message_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (account_id, key)
);

-- Work queue for vendor connections. A row is leased while being sent and deleted once the vendor answers.
CREATE TABLE send_queue (
    id            BIGSERIAL PRIMARY KEY,
    message_id    UUID NOT NULL UNIQUE,
    created_at    TIMESTAMPTZ NOT NULL,
    connection_id BIGINT NOT NULL,
    available_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    leased_until  TIMESTAMPTZ
);
CREATE INDEX send_queue_ready_idx ON send_queue (connection_id, available_at);

-- Vendor message ID -> our message, for matching DLRs. One row per submitted part.
CREATE TABLE vendor_message_ids (
    connection_id BIGINT NOT NULL,
    vendor_id     TEXT NOT NULL,
    message_id    UUID NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (connection_id, vendor_id)
);
CREATE INDEX vendor_message_ids_created_idx ON vendor_message_ids (created_at);

-- DLRs waiting to be delivered to a client (SMPP client not bound, or webhook failing).
CREATE TABLE dlr_outbox (
    id         BIGSERIAL PRIMARY KEY,
    message_id UUID NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    account_id BIGINT NOT NULL,
    attempts   INT NOT NULL DEFAULT 0,
    next_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL DEFAULT now() + INTERVAL '3 days'
);
CREATE INDEX dlr_outbox_account_idx ON dlr_outbox (account_id, next_at);

-- ---------------------------------------------------------------------------------------------------------
-- Statistics: one row per hour and dimension combination, updated continuously by the engine
-- ---------------------------------------------------------------------------------------------------------
CREATE TABLE stats_hourly (
    hour          TIMESTAMPTZ NOT NULL,
    client_id     BIGINT NOT NULL DEFAULT 0,
    connection_id BIGINT NOT NULL DEFAULT 0,
    country_iso   TEXT NOT NULL DEFAULT '',
    network_id    BIGINT NOT NULL DEFAULT 0,
    submitted     BIGINT NOT NULL DEFAULT 0,
    rejected      BIGINT NOT NULL DEFAULT 0,
    sent          BIGINT NOT NULL DEFAULT 0,
    failed        BIGINT NOT NULL DEFAULT 0,
    delivered     BIGINT NOT NULL DEFAULT 0,
    undelivered   BIGINT NOT NULL DEFAULT 0,
    parts         BIGINT NOT NULL DEFAULT 0,
    revenue       NUMERIC(20, 6) NOT NULL DEFAULT 0,
    cost          NUMERIC(20, 6) NOT NULL DEFAULT 0,
    dlr_latency_ms_sum BIGINT NOT NULL DEFAULT 0,
    dlr_latency_count  BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (hour, client_id, connection_id, country_iso, network_id)
);
CREATE INDEX stats_hourly_client_idx ON stats_hourly (client_id, hour);
CREATE INDEX stats_hourly_connection_idx ON stats_hourly (connection_id, hour);

-- Key/value settings editable from the portal.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
