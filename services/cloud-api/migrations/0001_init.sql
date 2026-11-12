-- AeroLab cloud schema v1 (PostgreSQL). Tenant isolation: every site-scoped
-- table carries site_id and every query filters by a site the caller is a
-- member of. Idempotency: ingest keys are PRIMARY KEYs.

CREATE TABLE organizations (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE cloud_users (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE CHECK (email = lower(email)),
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    disabled_at   TIMESTAMPTZ
);

CREATE TABLE organization_members (
    org_id  TEXT NOT NULL REFERENCES organizations(id),
    user_id TEXT NOT NULL REFERENCES cloud_users(id),
    role    TEXT NOT NULL CHECK (role IN ('OWNER','MEMBER')),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE sites (
    id               TEXT PRIMARY KEY CHECK (id ~ '^[a-z0-9][a-z0-9-]{2,39}$'),
    org_id           TEXT NOT NULL REFERENCES organizations(id),
    name             TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_edge_seen_at TIMESTAMPTZ,
    last_sync_at     TIMESTAMPTZ,
    edge_version     TEXT
);

-- Authorization lives on the SITE membership (spec 7.1): Admin at A may be Viewer at B.
CREATE TABLE site_members (
    site_id TEXT NOT NULL REFERENCES sites(id),
    user_id TEXT NOT NULL REFERENCES cloud_users(id),
    role    TEXT NOT NULL CHECK (role IN ('ADMIN','OPERATOR','VIEWER')),
    PRIMARY KEY (site_id, user_id)
);
CREATE INDEX ix_site_members_user ON site_members(user_id);

-- Per-site edge credential: only the hash is stored, tokens are rotatable/revocable.
CREATE TABLE site_edge_credentials (
    id         TEXT PRIMARY KEY,
    site_id    TEXT NOT NULL REFERENCES sites(id),
    token_hash TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ
);
CREATE INDEX ix_edge_cred_site ON site_edge_credentials(site_id);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES cloud_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ
);

-- Current truth pushed by the edge (P0); replaced on every sync.
CREATE TABLE latest_site_status (
    site_id     TEXT PRIMARY KEY REFERENCES sites(id),
    state       JSONB NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,   -- edge clock when it built the snapshot
    received_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE units (
    site_id    TEXT NOT NULL REFERENCES sites(id),
    id         TEXT NOT NULL,
    name       TEXT NOT NULL,
    view       JSONB NOT NULL,          -- the edge's unit view, verbatim
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, id)
);

-- Immutable device records, idempotent by the edge's stable event id.
CREATE TABLE telemetry_snapshots (
    site_id         TEXT NOT NULL REFERENCES sites(id),
    event_id        TEXT NOT NULL,
    unit_id         TEXT,
    device_id       TEXT NOT NULL,
    boot_id         TEXT NOT NULL,
    sequence_number BIGINT NOT NULL,
    sampled_at_utc  TIMESTAMPTZ,                -- device clock if it had one, else NULL (never invented)
    clock_quality   TEXT NOT NULL,
    effective_at    TIMESTAMPTZ NOT NULL,        -- edge's best placement, with time_quality
    time_quality    TEXT NOT NULL,
    edge_recorded_at TIMESTAMPTZ NOT NULL,
    cloud_ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),   -- separate from source time
    source          TEXT NOT NULL CHECK (source IN ('DEVICE','SIMULATOR')),
    readings        JSONB NOT NULL,
    PRIMARY KEY (site_id, event_id),
    UNIQUE (site_id, device_id, boot_id, sequence_number)
);
CREATE INDEX ix_cloud_tel_unit_time ON telemetry_snapshots(site_id, unit_id, effective_at);

CREATE TABLE device_events (
    site_id         TEXT NOT NULL REFERENCES sites(id),
    event_id        TEXT NOT NULL,
    unit_id         TEXT,
    device_id       TEXT NOT NULL,
    boot_id         TEXT NOT NULL,
    sequence_number BIGINT NOT NULL,
    event_type      TEXT NOT NULL,
    severity        TEXT NOT NULL,
    code            TEXT NOT NULL,
    details         JSONB NOT NULL DEFAULT '{}',
    effective_at    TIMESTAMPTZ NOT NULL,
    time_quality    TEXT NOT NULL,
    edge_recorded_at TIMESTAMPTZ NOT NULL,
    cloud_ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    source          TEXT NOT NULL,
    PRIMARY KEY (site_id, event_id),
    UNIQUE (site_id, device_id, boot_id, sequence_number)
);

-- Incidents merge by stable id; the update with the greatest changed_at wins.
CREATE TABLE incidents (
    site_id    TEXT NOT NULL REFERENCES sites(id),
    id         TEXT NOT NULL,
    unit_id    TEXT NOT NULL,
    severity   TEXT NOT NULL,
    code       TEXT NOT NULL,
    status     TEXT NOT NULL,
    data       JSONB NOT NULL,
    changed_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (site_id, id)
);
CREATE INDEX ix_cloud_incidents_status ON incidents(site_id, status);

-- Remote programme REQUESTS. Never actuation.
CREATE TABLE remote_command_intents (
    id            TEXT PRIMARY KEY,
    site_id       TEXT NOT NULL REFERENCES sites(id),
    unit_id       TEXT NOT NULL,
    requested_by  TEXT NOT NULL REFERENCES cloud_users(id),
    expected_program_version INTEGER NOT NULL CHECK (expected_program_version >= 0),
    program       JSONB NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('PENDING_SITE','PENDING_DEVICE','APPLIED','REJECTED','CONFLICT','EXPIRED','UNKNOWN')),
    reason        TEXT,
    applied_version INTEGER,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ix_intents_site_status ON remote_command_intents(site_id, status);
-- one in-flight remote change per unit (pilot rule)
CREATE UNIQUE INDEX ux_intent_one_inflight ON remote_command_intents(site_id, unit_id) WHERE status IN ('PENDING_SITE','PENDING_DEVICE');

CREATE TABLE site_sync_ingest (
    site_id     TEXT NOT NULL REFERENCES sites(id),
    batch_id    TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    accepted    INTEGER NOT NULL,
    duplicates  INTEGER NOT NULL,
    rejected    INTEGER NOT NULL,
    PRIMARY KEY (site_id, batch_id)
);

CREATE TABLE audit_events (
    id          BIGSERIAL PRIMARY KEY,
    site_id     TEXT,
    actor_id    TEXT,
    actor_kind  TEXT NOT NULL,
    action      TEXT NOT NULL,
    target      TEXT,
    detail      JSONB,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX ix_cloud_audit_site ON audit_events(site_id, occurred_at);
