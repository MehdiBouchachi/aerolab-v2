-- Relay: the cloud-served dashboard forwards a site API call to the Pi, which runs it through its own
-- handler (contracts/sync RelayRequest). Rows are short-lived; the sweeper deletes them after an hour.
CREATE TABLE relay_requests (
    id              TEXT PRIMARY KEY,
    site_id         TEXT NOT NULL REFERENCES sites(id),
    requested_by    TEXT NOT NULL REFERENCES cloud_users(id),
    role            TEXT NOT NULL CHECK (role IN ('ADMIN','OPERATOR','VIEWER')),
    method          TEXT NOT NULL CHECK (method IN ('GET','POST','PUT','PATCH','DELETE')),
    path            TEXT NOT NULL,
    body            TEXT,
    -- PENDING: waiting for the Pi. CLAIMED: the Pi took it (it may run). DONE: the Pi answered.
    -- EXPIRED: never picked up, and now it never will be.
    status          TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','CLAIMED','DONE','EXPIRED')),
    response_status INT,
    response_body   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL,
    done_at         TIMESTAMPTZ
);
CREATE INDEX ix_relay_pending ON relay_requests(site_id, created_at) WHERE status = 'PENDING';
CREATE INDEX ix_relay_created ON relay_requests(created_at);
