-- Self-service fleet management (docs/architecture/fleet-management.md).
--
-- 1. Units added from the UI: the edge creates the controller's broker credential itself. Only the
--    Mosquitto-format hash is stored; the password is shown to the person adding the unit exactly once.
ALTER TABLE devices ADD COLUMN broker_password_hash TEXT;
ALTER TABLE devices ADD COLUMN credentials_issued_at TEXT;

-- 2. Crop profiles. Every number in a profile is typed in by the grower or their agronomist; this
--    repository ships NO crop values. value_source records who supplied them.
CREATE TABLE crop_profiles (
    id              TEXT PRIMARY KEY,
    site_id         TEXT NOT NULL REFERENCES sites(id),
    name            TEXT NOT NULL,
    crop            TEXT NOT NULL,
    variety         TEXT,
    notes           TEXT,
    value_source    TEXT NOT NULL,
    thresholds_json TEXT NOT NULL DEFAULT '{}',
    program_json    TEXT,
    version         INTEGER NOT NULL DEFAULT 1 CHECK (version >= 1),
    created_by      TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    archived_at     TEXT
) STRICT;
CREATE UNIQUE INDEX ux_crop_profile_name ON crop_profiles(site_id, name) WHERE archived_at IS NULL;

ALTER TABLE growing_units ADD COLUMN crop_profile_id TEXT REFERENCES crop_profiles(id);
ALTER TABLE growing_units ADD COLUMN crop_assigned_at TEXT;
-- Retired units keep their history but leave every list, alert and the cloud view.
ALTER TABLE growing_units ADD COLUMN retired_at TEXT;

-- 3. Cloud link created at runtime by "Connect this Pi" (claim code), so no one has to edit env files.
--    The token is the edge's own credential for the cloud; the database file is 0600.
CREATE TABLE cloud_link (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    url        TEXT NOT NULL,
    token      TEXT NOT NULL,
    org_name   TEXT,
    linked_by  TEXT NOT NULL,
    linked_at  TEXT NOT NULL
) STRICT;
