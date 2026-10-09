-- Phase 2: blacklists, incoming (MO) message routing, two-factor login, DLR-based failover.

-- Numbers that must never receive messages. client_id NULL = blocked for every client.
CREATE TABLE blacklist (
    id         BIGSERIAL PRIMARY KEY,
    client_id  BIGINT REFERENCES clients (id) ON DELETE CASCADE,
    number     TEXT NOT NULL,
    reason     TEXT NOT NULL DEFAULT '',
    created_by BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX blacklist_key ON blacklist (COALESCE(client_id, 0), number);

-- Where incoming messages (replies to short codes / long numbers) go.
CREATE TABLE mo_routes (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    priority       INT NOT NULL DEFAULT 100,
    -- the number the subscriber wrote to (short code or long number); prefix match, empty = any
    number_prefix  TEXT NOT NULL DEFAULT '',
    -- first word of the text, case-insensitive; empty = any
    keyword        TEXT NOT NULL DEFAULT '',
    client_id      BIGINT NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    -- SMPP account that receives the message as deliver_sm; without one the client's MO webhook is used
    account_id     BIGINT REFERENCES accounts (id) ON DELETE SET NULL,
    -- STOP / UNSUBSCRIBE replies add the sender to the client's blacklist
    auto_opt_out   BOOLEAN NOT NULL DEFAULT true,
    status         TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE clients ADD COLUMN mo_webhook_url TEXT NOT NULL DEFAULT '';

-- Two-factor login (TOTP). The secret is stored encrypted.
ALTER TABLE users ADD COLUMN totp_secret_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled BOOLEAN NOT NULL DEFAULT false;

-- Failover after a negative DLR: these DLR statuses (e.g. UNDELIV, REJECTD) or "STAT:ERR" pairs
-- (e.g. UNDELIV:011) make the gateway resend the message through the next vendor in the route,
-- without charging the client again. Empty = never.
ALTER TABLE connections ADD COLUMN failover_on_dlr TEXT[] NOT NULL DEFAULT '{}';

-- MO messages carry the account they were delivered to.
CREATE INDEX messages_direction_idx ON messages (direction, created_at DESC);
