-- Site identity (Ed25519) lets a phone verify it is talking to the SAME Pi it was paired with, without
-- any shared long-term secret in a QR code. Pairing codes are short-lived and single-use; only their hash is stored.
CREATE TABLE site_identity (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    public_key  BLOB NOT NULL,
    private_key BLOB NOT NULL,
    created_at  TEXT NOT NULL
) STRICT;

CREATE TABLE pairing_codes (
    code_hash  TEXT PRIMARY KEY,
    created_by TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT,
    device_name TEXT
) STRICT;
