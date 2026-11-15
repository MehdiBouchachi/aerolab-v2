-- Self-service fleets (docs/architecture/fleet-management.md).
-- One organisation model for every market: a household (PERSONAL), a farm business or cooperative
-- (BUSINESS), or a public body (GOVERNMENT). The kind changes defaults and wording, never the security
-- model: authorization stays on the site membership.
ALTER TABLE organizations ADD COLUMN kind TEXT NOT NULL DEFAULT 'BUSINESS' CHECK (kind IN ('PERSONAL','BUSINESS','GOVERNMENT'));

-- "Connect a Pi": an org owner creates a short-lived, single-use code; the Pi presents it once and
-- receives its own edge token. Only the hash of the code is stored.
CREATE TABLE site_claim_codes (
    code_hash  TEXT PRIMARY KEY,
    org_id     TEXT NOT NULL REFERENCES organizations(id),
    created_by TEXT NOT NULL REFERENCES cloud_users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,
    site_id    TEXT REFERENCES sites(id)
);
CREATE INDEX ix_claim_org ON site_claim_codes(org_id, created_at);
CREATE INDEX ix_sites_org ON sites(org_id);
CREATE INDEX ix_org_members_user ON organization_members(user_id);
