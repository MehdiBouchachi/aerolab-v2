-- AeroLab edge schema v1 (SQLite, WAL). Invariants live in the schema:
-- CHECK constraints for enums, UNIQUE for dedup, FKs for ownership.

CREATE TABLE sites (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    simulated   INTEGER NOT NULL DEFAULT 0 CHECK (simulated IN (0,1)),
    cloud_linked INTEGER NOT NULL DEFAULT 0 CHECK (cloud_linked IN (0,1)),
    last_cloud_sync_at TEXT,
    created_at  TEXT NOT NULL
) STRICT;

CREATE TABLE devices (
    id               TEXT PRIMARY KEY,
    site_id          TEXT NOT NULL REFERENCES sites(id),
    unit_id          TEXT UNIQUE,
    hardware_profile TEXT,
    firmware_version TEXT,
    data_source      TEXT NOT NULL CHECK (data_source IN ('DEVICE','SIMULATOR')),
    provisioned_at   TEXT NOT NULL,
    disabled_at      TEXT
) STRICT;

CREATE TABLE growing_units (
    id           TEXT PRIMARY KEY,
    site_id      TEXT NOT NULL REFERENCES sites(id),
    device_id    TEXT UNIQUE REFERENCES devices(id),
    name         TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'UNKNOWN',
    applied_program_version INTEGER,
    last_seen_at TEXT,
    created_at   TEXT NOT NULL
) STRICT;
CREATE INDEX ix_units_site ON growing_units(site_id, name);

-- Immutable device records. effective_at_utc is what charts use; time_quality
-- says how it was obtained. sampled_at_utc / uptime_ms are never altered.
CREATE TABLE telemetry_snapshots (
    event_id        TEXT PRIMARY KEY,
    site_id         TEXT NOT NULL,
    device_id       TEXT NOT NULL,
    unit_id         TEXT,
    boot_id         TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    uptime_ms       INTEGER NOT NULL,
    sampled_at_utc  TEXT,
    clock_quality   TEXT NOT NULL,
    received_at_utc TEXT NOT NULL,
    effective_at_utc TEXT NOT NULL,
    time_quality    TEXT NOT NULL CHECK (time_quality IN ('DEVICE_CLOCK','ESTIMATED_FROM_UPTIME','RECEIVED_AT_ONLY')),
    program_version_applied INTEGER,
    readings_json   TEXT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('DEVICE','SIMULATOR')),
    UNIQUE (device_id, boot_id, sequence_number)
) STRICT;
CREATE INDEX ix_telemetry_unit_time ON telemetry_snapshots(unit_id, effective_at_utc);
CREATE INDEX ix_telemetry_received ON telemetry_snapshots(received_at_utc);

CREATE TABLE device_events (
    event_id        TEXT PRIMARY KEY,
    site_id         TEXT NOT NULL,
    device_id       TEXT NOT NULL,
    unit_id         TEXT,
    boot_id         TEXT NOT NULL,
    sequence_number INTEGER NOT NULL,
    uptime_ms       INTEGER NOT NULL,
    sampled_at_utc  TEXT,
    clock_quality   TEXT NOT NULL,
    received_at_utc TEXT NOT NULL,
    effective_at_utc TEXT NOT NULL,
    time_quality    TEXT NOT NULL CHECK (time_quality IN ('DEVICE_CLOCK','ESTIMATED_FROM_UPTIME','RECEIVED_AT_ONLY')),
    event_type      TEXT NOT NULL,
    severity        TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
    code            TEXT NOT NULL,
    details_json    TEXT NOT NULL,
    source          TEXT NOT NULL CHECK (source IN ('DEVICE','SIMULATOR')),
    UNIQUE (device_id, boot_id, sequence_number)
) STRICT;
CREATE INDEX ix_events_unit_time ON device_events(unit_id, effective_at_utc);
CREATE INDEX ix_events_received ON device_events(received_at_utc);

-- A programme revision is only APPLIED when the ESP32 confirmed it.
CREATE TABLE programs (
    unit_id      TEXT NOT NULL REFERENCES growing_units(id),
    version      INTEGER NOT NULL CHECK (version >= 1),
    payload_json TEXT NOT NULL,
    created_by   TEXT NOT NULL,
    command_id   TEXT,
    requested_at TEXT NOT NULL,
    applied_at   TEXT,
    status       TEXT NOT NULL CHECK (status IN ('REQUESTED','APPLIED','REJECTED','SUPERSEDED','EXPIRED','UNKNOWN')),
    PRIMARY KEY (unit_id, version)
) STRICT;

CREATE TABLE commands (
    id          TEXT PRIMARY KEY,
    site_id     TEXT NOT NULL,
    unit_id     TEXT NOT NULL REFERENCES growing_units(id),
    device_id   TEXT NOT NULL,
    requested_by TEXT NOT NULL,
    command_type TEXT NOT NULL CHECK (command_type IN ('APPLY_PROGRAM','QUERY_STATE')),
    expected_program_version INTEGER NOT NULL,
    new_program_version INTEGER,
    expires_at_utc TEXT NOT NULL,
    request_json TEXT NOT NULL,
    result_json  TEXT,
    status      TEXT NOT NULL CHECK (status IN ('PENDING','SENT','APPLIED','REJECTED','CONFLICT','EXPIRED','UNKNOWN')),
    reason_code TEXT,
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_sent_at TEXT,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
) STRICT;
CREATE INDEX ix_commands_unit_status ON commands(unit_id, status, created_at);
-- only one in-flight programme change per unit (pilot rule: one unit at a time)
CREATE UNIQUE INDEX ux_commands_one_inflight ON commands(unit_id) WHERE status IN ('PENDING','SENT');

CREATE TABLE incidents (
    id            TEXT PRIMARY KEY,
    site_id       TEXT NOT NULL,
    unit_id       TEXT NOT NULL REFERENCES growing_units(id),
    severity      TEXT NOT NULL CHECK (severity IN ('info','warning','critical')),
    code          TEXT NOT NULL,
    status        TEXT NOT NULL CHECK (status IN ('ACTIVE_UNACKNOWLEDGED','ACTIVE_ACKNOWLEDGED','RESOLVED_UNACKNOWLEDGED','RESOLVED_ACKNOWLEDGED')),
    first_seen_at TEXT NOT NULL,
    last_seen_at  TEXT NOT NULL,
    occurrences   INTEGER NOT NULL DEFAULT 1,
    acknowledged_by TEXT,
    acknowledged_at TEXT,
    resolved_at   TEXT,
    details_json  TEXT NOT NULL DEFAULT '{}'
) STRICT;
CREATE INDEX ix_incident_open ON incidents(site_id, status, first_seen_at);
-- repeating conditions update ONE active incident, never insert thousands
CREATE UNIQUE INDEX ux_incident_active ON incidents(unit_id, code) WHERE status IN ('ACTIVE_UNACKNOWLEDGED','ACTIVE_ACKNOWLEDGED');

-- transactional outbox for cloud sync (written only when cloud_linked)
CREATE TABLE outbox (
    event_id    TEXT PRIMARY KEY,
    event_type  TEXT NOT NULL,
    priority    INTEGER NOT NULL CHECK (priority BETWEEN 0 AND 3),
    site_id     TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    attempts    INTEGER NOT NULL DEFAULT 0,
    last_attempt_at TEXT,
    cloud_ack_at TEXT
) STRICT;
CREATE INDEX ix_outbox_pending ON outbox(cloud_ack_at, priority, created_at);

CREATE TABLE local_users (
    id            TEXT PRIMARY KEY,
    site_id       TEXT NOT NULL REFERENCES sites(id),
    username      TEXT NOT NULL,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('ADMIN','OPERATOR','VIEWER')),
    disabled_at   TEXT,
    created_at    TEXT NOT NULL,
    UNIQUE (site_id, username)
) STRICT;

-- only the SHA-256 of the bearer token is stored
CREATE TABLE local_sessions (
    token_hash  TEXT PRIMARY KEY,
    user_id     TEXT NOT NULL REFERENCES local_users(id),
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    absolute_expires_at TEXT NOT NULL,
    revoked_at  TEXT
) STRICT;
CREATE INDEX ix_sessions_user ON local_sessions(user_id);

CREATE TABLE audit_events (
    id          TEXT PRIMARY KEY,
    site_id     TEXT NOT NULL,
    actor_id    TEXT,
    actor_source TEXT NOT NULL,
    action      TEXT NOT NULL,
    target_type TEXT,
    target_id   TEXT,
    previous_json TEXT,
    next_json   TEXT,
    occurred_at TEXT NOT NULL
) STRICT;
CREATE INDEX ix_audit_time ON audit_events(site_id, occurred_at);
