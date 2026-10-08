-- Supply store, design 11 V0.2.1 §2.4 (phase 1). Observed, provider-verified
-- facts. Sole writer: the supply engine. Configuration activation and the
-- ledger never write these tables; the supply engine never writes theirs.

-- A credential may only reference a secret of its own namespace. secret_ref is
-- already secret_refs' primary key, so this index adds no new constraint on
-- existing rows; it only lets the composite foreign key below bind namespace.
CREATE UNIQUE INDEX secret_refs_namespace_ref ON secret_refs(namespace, secret_ref);
CREATE TABLE supply_credentials (
  namespace TEXT NOT NULL, credential_id TEXT NOT NULL, provider_id TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN('api_key','oauth','session','sts','console_token','none')),
  origin TEXT NOT NULL CHECK(origin IN('found','added')),
  strategy TEXT NOT NULL CHECK(length(strategy) BETWEEN 1 AND 64),
  locator_json TEXT NOT NULL CHECK(json_valid(locator_json)),
  secret_ref TEXT,
  account_id TEXT,
  status TEXT NOT NULL CHECK(status IN('unverified','ok','signed_out','error','hidden','needs_key')),
  status_detail TEXT NOT NULL DEFAULT '', verified_at INTEGER, created_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, credential_id),
  FOREIGN KEY(namespace, secret_ref) REFERENCES secret_refs(namespace, secret_ref),
  -- found secrets stay where they live; added ones are encrypted or still need a key
  CHECK(origin = 'added' OR secret_ref IS NULL),
  CHECK(origin = 'found' OR kind = 'none' OR secret_ref IS NOT NULL OR status = 'needs_key'),
  FOREIGN KEY(namespace, account_id) REFERENCES supply_accounts(namespace, account_id)
) STRICT;
CREATE TABLE supply_accounts (
  namespace TEXT NOT NULL, account_id TEXT NOT NULL, provider_id TEXT NOT NULL,
  provider_identity TEXT NOT NULL CHECK(length(provider_identity) > 0),
  display_name TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, account_id), UNIQUE(namespace, provider_id, provider_identity)
) STRICT;
CREATE TABLE supply_packages (
  namespace TEXT NOT NULL, package_id TEXT NOT NULL, account_id TEXT NOT NULL,
  template_ref TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN('subscription','prepaid','payg','free_grant','credit_pack')),
  basis TEXT NOT NULL CHECK(basis IN('observed','declared')),
  plan TEXT NOT NULL DEFAULT '', starts_at INTEGER, renews_at INTEGER, ends_at INTEGER,
  auto_renew INTEGER CHECK(auto_renew IN(0,1)), live INTEGER NOT NULL CHECK(live IN(0,1)),
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json)), updated_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, package_id), UNIQUE(namespace, account_id, template_ref),
  FOREIGN KEY(namespace, account_id) REFERENCES supply_accounts(namespace, account_id)
) STRICT;
CREATE TABLE supply_resources (
  namespace TEXT NOT NULL, resource_id TEXT NOT NULL, physical_key TEXT NOT NULL,
  package_id TEXT NOT NULL, template_ref TEXT NOT NULL,
  metric_id TEXT NOT NULL, unit TEXT NOT NULL, quantum_exp INTEGER NOT NULL, currency TEXT NOT NULL DEFAULT '',
  semantics_ref TEXT NOT NULL, semantics_revision TEXT NOT NULL,
  window_json TEXT NOT NULL CHECK(json_valid(window_json)),
  scope_json TEXT NOT NULL CHECK(json_valid(scope_json)),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY(namespace, resource_id),
  -- the key's value is derived globally (provider, identity, templates, scope);
  -- each namespace stores its own observation of it (design 11 §2.2)
  UNIQUE(namespace, physical_key),
  FOREIGN KEY(namespace, package_id) REFERENCES supply_packages(namespace, package_id)
) STRICT;
CREATE TABLE collector_runs (
  namespace TEXT NOT NULL, run_id TEXT NOT NULL, credential_id TEXT NOT NULL,
  capability TEXT NOT NULL CHECK(capability IN('inventory','models','verify')),
  started_at INTEGER NOT NULL, finished_at INTEGER,
  outcome TEXT CHECK(outcome IN('ok','signed_out','error','refused')),
  detail TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(namespace, run_id),
  CHECK((finished_at IS NULL) = (outcome IS NULL)),
  FOREIGN KEY(namespace, credential_id) REFERENCES supply_credentials(namespace, credential_id)
) STRICT;
CREATE INDEX collector_runs_latest ON collector_runs(namespace, credential_id, capability, started_at DESC);
-- Each reading is an immutable fact snapshot (§2.2.1): appended only when the
-- value changes; factId = resource_id || '@' || reading_id.
CREATE TABLE supply_readings (
  namespace TEXT NOT NULL, reading_id TEXT NOT NULL, resource_id TEXT NOT NULL,
  observed_at INTEGER NOT NULL, window_start INTEGER, window_end INTEGER,
  reset_basis TEXT NOT NULL CHECK(reset_basis IN('reported','computed','none')),
  limit_units INTEGER, used_units INTEGER, remaining_units INTEGER,
  run_id TEXT NOT NULL, evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
  PRIMARY KEY(namespace, reading_id),
  CHECK(window_end IS NULL OR window_start IS NULL OR window_start < window_end),
  CHECK(limit_units IS NOT NULL OR used_units IS NOT NULL OR remaining_units IS NOT NULL),
  FOREIGN KEY(namespace, resource_id) REFERENCES supply_resources(namespace, resource_id),
  FOREIGN KEY(namespace, run_id) REFERENCES collector_runs(namespace, run_id)
) STRICT;
CREATE INDEX supply_readings_latest ON supply_readings(namespace, resource_id, observed_at DESC);
CREATE TRIGGER supply_readings_no_update BEFORE UPDATE ON supply_readings
BEGIN SELECT RAISE(ABORT, 'supply_readings is append-only'); END;
CREATE TRIGGER supply_readings_no_delete BEFORE DELETE ON supply_readings
BEGIN SELECT RAISE(ABORT, 'supply_readings is append-only'); END;
