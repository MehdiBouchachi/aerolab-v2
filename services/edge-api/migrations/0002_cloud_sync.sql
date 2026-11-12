-- Cloud sync support: remember what the cloud was last told about a
-- cloud-originated command so status is reported exactly when it changes.
ALTER TABLE commands ADD COLUMN cloud_reported_status TEXT;
CREATE INDEX ix_commands_cloud_unreported ON commands(requested_by, status) WHERE requested_by LIKE 'cloud:%';
