-- Owner and active-revision index for write-only secret metadata.
-- Legacy secret_records remain decryptable for already-activated configs,
-- but have no owner row and therefore cannot be exposed or rotated via API.
CREATE TABLE secret_refs (
  secret_ref TEXT PRIMARY KEY,
  principal_id TEXT NOT NULL REFERENCES principals(principal_id),
  namespace TEXT NOT NULL,
  current_revision TEXT NOT NULL,
  mask TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  FOREIGN KEY(secret_ref, current_revision) REFERENCES secret_records(secret_ref, revision)
) STRICT;
CREATE INDEX secret_refs_owner ON secret_refs(principal_id, namespace, secret_ref);
