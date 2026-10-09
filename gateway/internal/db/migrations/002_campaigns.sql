-- Bulk campaigns sent from the portal (uploaded number lists).
CREATE TABLE campaigns (
    id          BIGSERIAL PRIMARY KEY,
    client_id   BIGINT NOT NULL REFERENCES clients (id) ON DELETE CASCADE,
    account_id  BIGINT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    name        TEXT NOT NULL DEFAULT '',
    sender      TEXT NOT NULL DEFAULT '',
    body        TEXT NOT NULL,
    total       INT NOT NULL DEFAULT 0,
    processed   INT NOT NULL DEFAULT 0,
    accepted    INT NOT NULL DEFAULT 0,
    rejected    INT NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'done', 'failed', 'cancelled')),
    last_error  TEXT NOT NULL DEFAULT '',
    created_by  BIGINT REFERENCES users (id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ
);
CREATE INDEX campaigns_client_idx ON campaigns (client_id, created_at DESC);

-- Numbers waiting to be sent for a campaign; rows are deleted as they are processed.
CREATE TABLE campaign_numbers (
    campaign_id BIGINT NOT NULL REFERENCES campaigns (id) ON DELETE CASCADE,
    seq         INT NOT NULL,
    number      TEXT NOT NULL,
    PRIMARY KEY (campaign_id, seq)
);
