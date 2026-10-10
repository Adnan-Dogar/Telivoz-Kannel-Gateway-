-- White-label branding per portal domain (a reseller's own domain); the row without a domain is the default.
CREATE TABLE branding (
    id            BIGSERIAL PRIMARY KEY,
    domain        TEXT CHECK (domain = lower(domain) AND domain !~ '[/: ]'),
    name          TEXT NOT NULL,
    tagline       TEXT,
    logo          TEXT CHECK (logo ~ '^data:image/(png|jpeg|webp|svg\+xml);base64,' AND length(logo) <= 400000),
    primary_color TEXT CHECK (primary_color ~ '^#[0-9a-fA-F]{6}$'),
    support_email TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX branding_domain_key ON branding (COALESCE(domain, ''));
