-- ANT initial schema (V0.7 contract): 34 STRICT tables with CHECK guards,
-- append-only event tables and terminal-operation immutability triggers.
--
-- Canonical source: docs/design/sql/001-initial.sql, adapted for the runtime
-- migrator (server/internal/store/sqlite/migrate.go) as follows:
--   * "PRAGMA foreign_keys = ON", "BEGIN IMMEDIATE" and "COMMIT" removed --
--     the migrator runs this script inside its own BEGIN IMMEDIATE
--     transaction with foreign_keys enabled via DSN pragmas.
--   * The trailing "INSERT INTO schema_migrations(version, applied_at)"
--     removed -- the migrator writes the version row itself once the whole
--     script has executed, so a failed script leaves no version behind.
--
-- go:embed cannot reference parent directories, so a byte-identical copy of
-- this file lives at server/internal/store/sqlite/001_initial.sql and is what
-- the migrator actually embeds. Keep the two in sync:
-- server/tests/integration/migrate_test.go fails when they diverge.

CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL) STRICT;
CREATE TABLE runtime_control (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  restore_guard INTEGER NOT NULL DEFAULT 0 CHECK(restore_guard IN (0,1)),
  control_version INTEGER NOT NULL DEFAULT 1,
  last_clock_ms INTEGER NOT NULL DEFAULT 0
) STRICT;
INSERT INTO runtime_control(singleton) VALUES(1);
CREATE TABLE config_revisions (
  revision TEXT PRIMARY KEY, definition_json TEXT NOT NULL CHECK(json_valid(definition_json)),
  created_at INTEGER NOT NULL
) STRICT;
CREATE TABLE active_config (
  singleton INTEGER PRIMARY KEY CHECK(singleton=1),
  revision TEXT NOT NULL REFERENCES config_revisions(revision), version INTEGER NOT NULL
) STRICT;
CREATE TABLE accounts (
  account_id TEXT PRIMARY KEY, provider_owner_ref TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), version INTEGER NOT NULL,
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json))
) STRICT;
CREATE TABLE bindings (
  binding_id TEXT PRIMARY KEY, account_id TEXT NOT NULL REFERENCES accounts(account_id),
  adapter_ref TEXT NOT NULL, domain TEXT NOT NULL CHECK(length(domain)>0),
  endpoint TEXT NOT NULL, credential_ref TEXT NOT NULL, credential_revision TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), version INTEGER NOT NULL,
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json)), UNIQUE(binding_id,account_id)
) STRICT;
CREATE TABLE principals (
  principal_id TEXT PRIMARY KEY, display_name TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), created_at INTEGER NOT NULL
) STRICT;
CREATE TABLE auth_sessions (
  token_hash TEXT PRIMARY KEY, principal_id TEXT NOT NULL REFERENCES principals(principal_id),
  kind TEXT NOT NULL CHECK(kind IN('session','api_token','bootstrap')),
  namespace TEXT NOT NULL, expires_at INTEGER NOT NULL, revoked_at INTEGER,
  csrf_hash TEXT, created_at INTEGER NOT NULL
) STRICT;
CREATE INDEX auth_sessions_principal ON auth_sessions(principal_id,expires_at);
CREATE TABLE secret_records (
  secret_ref TEXT NOT NULL, revision TEXT NOT NULL, nonce BLOB NOT NULL,
  ciphertext BLOB NOT NULL, master_key_ref TEXT NOT NULL, mask TEXT NOT NULL,
  created_at INTEGER NOT NULL, PRIMARY KEY(secret_ref,revision)
) STRICT;
CREATE TABLE namespace_grants (
  principal_id TEXT NOT NULL, namespace TEXT NOT NULL,
  permission TEXT NOT NULL CHECK(permission IN('execute','read','cancel','manage','configure')),
  scope_kind TEXT NOT NULL CHECK(scope_kind IN('namespace','binding','account','pool','offer')),
  scope_id TEXT NOT NULL, PRIMARY KEY(principal_id,namespace,permission,scope_kind,scope_id)
) STRICT;
CREATE TABLE resources (
  resource_id TEXT PRIMARY KEY, owner_kind TEXT NOT NULL CHECK(owner_kind IN('account','binding')),
  owner_id TEXT NOT NULL, metric_id TEXT NOT NULL, unit TEXT NOT NULL,
  quantum_exp INTEGER NOT NULL, kind TEXT NOT NULL CHECK(kind IN('counter','occupancy')),
  semantics_ref TEXT NOT NULL, semantics_revision TEXT NOT NULL,
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json))
) STRICT;
CREATE TABLE binding_resources (
  binding_id TEXT NOT NULL REFERENCES bindings(binding_id),
  resource_id TEXT NOT NULL REFERENCES resources(resource_id),
  PRIMARY KEY(binding_id,resource_id)
) STRICT;
CREATE TABLE resource_offers (
  offer_id TEXT PRIMARY KEY, binding_id TEXT NOT NULL REFERENCES bindings(binding_id),
  domain TEXT NOT NULL CHECK(length(domain)>0), capability_profile TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), version INTEGER NOT NULL CHECK(version>=1),
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json)), UNIQUE(offer_id,binding_id)
) STRICT;
CREATE TABLE offer_resources (
  offer_id TEXT NOT NULL REFERENCES resource_offers(offer_id),
  resource_id TEXT NOT NULL REFERENCES resources(resource_id), PRIMARY KEY(offer_id,resource_id)
) STRICT;
CREATE TABLE resource_pools (
  pool_id TEXT PRIMARY KEY, namespace TEXT NOT NULL, name TEXT NOT NULL,
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), version INTEGER NOT NULL CHECK(version>=1),
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json)), UNIQUE(pool_id,namespace)
) STRICT;
CREATE TABLE pool_members (
  pool_id TEXT NOT NULL REFERENCES resource_pools(pool_id),
  offer_id TEXT NOT NULL REFERENCES resource_offers(offer_id),
  priority INTEGER NOT NULL CHECK(priority>=0 AND priority<=2147483647),
  enabled INTEGER NOT NULL CHECK(enabled IN(0,1)), version INTEGER NOT NULL CHECK(version>=1),
  PRIMARY KEY(pool_id,offer_id)
) STRICT;
CREATE INDEX pool_members_order ON pool_members(pool_id,enabled,priority,offer_id);
CREATE TABLE config_drafts (
  draft_id TEXT PRIMARY KEY, principal_id TEXT NOT NULL REFERENCES principals(principal_id),
  base_revision TEXT REFERENCES config_revisions(revision), version INTEGER NOT NULL CHECK(version>=1),
  status TEXT NOT NULL CHECK(status IN('editing','validated','activated','archived')),
  definition_json TEXT NOT NULL CHECK(json_valid(definition_json)),
  issues_json TEXT NOT NULL CHECK(json_valid(issues_json)),
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
) STRICT;
CREATE TABLE resource_states (
  resource_id TEXT NOT NULL REFERENCES resources(resource_id), window_key TEXT NOT NULL,
  start_ms INTEGER, end_ms INTEGER,
  capacity_units INTEGER NOT NULL CHECK(capacity_units>=0),
  committed_units INTEGER NOT NULL DEFAULT 0 CHECK(committed_units>=0),
  adjustment_units INTEGER NOT NULL DEFAULT 0,
  reserved_units INTEGER NOT NULL DEFAULT 0 CHECK(reserved_units>=0),
  active_units INTEGER NOT NULL DEFAULT 0 CHECK(active_units>=0),
  uncertain_units INTEGER NOT NULL DEFAULT 0 CHECK(uncertain_units>=0),
  adjustment_version INTEGER NOT NULL DEFAULT 0, version INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(resource_id,window_key), CHECK(end_ms IS NULL OR start_ms<end_ms)
) STRICT;
CREATE TABLE operations (
  namespace TEXT NOT NULL, operation_id TEXT NOT NULL, pool_id TEXT NOT NULL, fingerprint TEXT NOT NULL,
  state TEXT NOT NULL CHECK(state IN('running','succeeded','failed','cancelled','unknown')),
  runtime_id TEXT NOT NULL, owner_id TEXT NOT NULL, generation INTEGER NOT NULL CHECK(generation>=1),
  lease_until INTEGER NOT NULL, deadline_at INTEGER NOT NULL,
  max_attempts INTEGER NOT NULL CHECK(max_attempts>=1), permits_issued INTEGER NOT NULL DEFAULT 0 CHECK(permits_issued>=0),
  strategy_json TEXT NOT NULL CHECK(json_valid(strategy_json)), cancel_requested_at INTEGER,
  visible_output_at INTEGER, result_json TEXT CHECK(result_json IS NULL OR json_valid(result_json)),
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, version INTEGER NOT NULL DEFAULT 1,
  PRIMARY KEY(namespace,operation_id), FOREIGN KEY(pool_id,namespace) REFERENCES resource_pools(pool_id,namespace), CHECK(permits_issued<=max_attempts),
  CHECK((state='running' AND result_json IS NULL) OR (state<>'running' AND result_json IS NOT NULL))
) STRICT;
CREATE INDEX operations_due ON operations(lease_until) WHERE state='running';
CREATE INDEX operations_runtime ON operations(runtime_id) WHERE state='running';
CREATE TABLE guard_states (
  scope TEXT PRIMARY KEY, evidence_id TEXT NOT NULL, revision INTEGER NOT NULL,
  allowed INTEGER NOT NULL CHECK(allowed IN(0,1)), observed_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL, source_ref TEXT NOT NULL,
  evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json))
) STRICT;
CREATE TABLE execution_plans (
  plan_id TEXT PRIMARY KEY, config_revision TEXT NOT NULL REFERENCES config_revisions(revision),
  plan_json TEXT NOT NULL CHECK(json_valid(plan_json)), plan_hash TEXT NOT NULL
) STRICT;
CREATE TABLE attempts (
  attempt_id TEXT PRIMARY KEY, namespace TEXT NOT NULL, operation_id TEXT NOT NULL,
  binding_id TEXT NOT NULL, account_id TEXT NOT NULL, offer_id TEXT NOT NULL,
  plan_id TEXT NOT NULL REFERENCES execution_plans(plan_id), generation INTEGER NOT NULL,
  request_digest TEXT NOT NULL, guard_refs_json TEXT NOT NULL CHECK(json_valid(guard_refs_json)),
  phase TEXT NOT NULL CHECK(phase IN('prepared','executing','terminal')),
  external_outcome TEXT CHECK(external_outcome IN('known_success','known_failure','not_executed','unknown')),
  report_json TEXT CHECK(report_json IS NULL OR json_valid(report_json)), report_hash TEXT,
  provider_request_id TEXT, lease_until INTEGER NOT NULL,
  created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, version INTEGER NOT NULL DEFAULT 1,
  FOREIGN KEY(namespace,operation_id) REFERENCES operations(namespace,operation_id),
  FOREIGN KEY(binding_id,account_id) REFERENCES bindings(binding_id,account_id),
  FOREIGN KEY(offer_id,binding_id) REFERENCES resource_offers(offer_id,binding_id)
) STRICT;
CREATE UNIQUE INDEX attempts_one_active ON attempts(namespace,operation_id) WHERE phase IN('prepared','executing');
CREATE TABLE reservations (
  reservation_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL REFERENCES attempts(attempt_id),
  resource_id TEXT NOT NULL, window_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN('counter','occupancy')), units INTEGER NOT NULL CHECK(units>=0),
  phase TEXT NOT NULL CHECK(phase IN('reserved','executing','settled','released','uncertain')),
  FOREIGN KEY(resource_id,window_key) REFERENCES resource_states(resource_id,window_key),
  UNIQUE(attempt_id,resource_id,window_key)
) STRICT;
CREATE INDEX reservations_pending ON reservations(resource_id,window_key,phase);
CREATE TABLE dispatch_permits (
  permit_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL UNIQUE REFERENCES attempts(attempt_id),
  runtime_id TEXT NOT NULL, generation INTEGER NOT NULL, control_version INTEGER NOT NULL,
  credential_revision TEXT NOT NULL, expires_at INTEGER NOT NULL,
  request_digest TEXT NOT NULL,
  status TEXT NOT NULL CHECK(status IN('granted','consumed','revoked')),
  granted_at INTEGER NOT NULL, consumed_at INTEGER
) STRICT;
CREATE TABLE command_receipts (
  scope TEXT NOT NULL, command_id TEXT NOT NULL, kind TEXT NOT NULL,
  input_hash TEXT NOT NULL, result_json TEXT NOT NULL CHECK(json_valid(result_json)),
  created_at INTEGER NOT NULL, PRIMARY KEY(scope,command_id)
) STRICT;
CREATE TABLE resource_events (
  event_id TEXT PRIMARY KEY, command_scope TEXT NOT NULL, command_id TEXT NOT NULL,
  resource_id TEXT NOT NULL, window_key TEXT NOT NULL, attempt_id TEXT REFERENCES attempts(attempt_id),
  event_kind TEXT NOT NULL CHECK(event_kind IN('reserve','activate','release','consume','encumber','adjust','reconcile','window_move')),
  committed_delta INTEGER NOT NULL DEFAULT 0, adjustment_delta INTEGER NOT NULL DEFAULT 0,
  reserved_delta INTEGER NOT NULL DEFAULT 0, active_delta INTEGER NOT NULL DEFAULT 0,
  uncertain_delta INTEGER NOT NULL DEFAULT 0, charged_at INTEGER,
  evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)), recorded_at INTEGER NOT NULL,
  FOREIGN KEY(resource_id,window_key) REFERENCES resource_states(resource_id,window_key),
  FOREIGN KEY(command_scope,command_id) REFERENCES command_receipts(scope,command_id) DEFERRABLE INITIALLY DEFERRED
) STRICT;
CREATE INDEX events_resource_time ON resource_events(resource_id,charged_at);
CREATE INDEX events_attempt ON resource_events(attempt_id);
CREATE TABLE encumbrances (
  encumbrance_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL REFERENCES attempts(attempt_id),
  resource_id TEXT NOT NULL REFERENCES resources(resource_id), window_key TEXT NOT NULL,
  kind TEXT NOT NULL CHECK(kind IN('occupancy','window_attribution')),
  units INTEGER NOT NULL CHECK(units>=0), status TEXT NOT NULL CHECK(status IN('open','released')),
  evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)), created_at INTEGER NOT NULL, released_at INTEGER,
  UNIQUE(attempt_id,resource_id,kind)
) STRICT;
CREATE INDEX encumbrances_open ON encumbrances(resource_id) WHERE status='open';
CREATE TABLE observations (
  observation_id TEXT PRIMARY KEY, resource_id TEXT NOT NULL, window_key TEXT NOT NULL,
  remaining_units INTEGER NOT NULL CHECK(remaining_units>=0), committed_at_start INTEGER NOT NULL,
  adjustment_version_at_start INTEGER NOT NULL, observed_at INTEGER NOT NULL, expires_at INTEGER NOT NULL,
  valid INTEGER NOT NULL CHECK(valid IN(0,1)), source_ref TEXT NOT NULL, evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
  FOREIGN KEY(resource_id,window_key) REFERENCES resource_states(resource_id,window_key)
) STRICT;
CREATE INDEX observations_latest ON observations(resource_id,window_key,observed_at DESC);
CREATE TABLE holds (
  hold_id TEXT PRIMARY KEY, scope_kind TEXT NOT NULL CHECK(scope_kind IN('account','binding','credential','resource','domain')),
  scope_id TEXT NOT NULL, reason TEXT NOT NULL, until_at INTEGER,
  status TEXT NOT NULL CHECK(status IN('open','cleared')), evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)),
  created_at INTEGER NOT NULL, cleared_at INTEGER
) STRICT;
CREATE INDEX holds_open ON holds(scope_kind,scope_id) WHERE status='open';
CREATE TABLE decision_records (
  decision_id TEXT PRIMARY KEY, namespace TEXT NOT NULL, operation_id TEXT NOT NULL,
  attempt_id TEXT REFERENCES attempts(attempt_id), stage TEXT NOT NULL CHECK(stage IN('prepare','rejected','dispatch')),
  snapshot_json TEXT NOT NULL CHECK(json_valid(snapshot_json)), trace_truncated INTEGER NOT NULL CHECK(trace_truncated IN(0,1)),
  created_at INTEGER NOT NULL, FOREIGN KEY(namespace,operation_id) REFERENCES operations(namespace,operation_id)
) STRICT;
CREATE INDEX decisions_operation ON decision_records(namespace,operation_id,created_at);
CREATE TABLE output_chunks (
  attempt_id TEXT NOT NULL REFERENCES attempts(attempt_id), sequence INTEGER NOT NULL CHECK(sequence>=0),
  data BLOB NOT NULL, sha256 TEXT NOT NULL, created_at INTEGER NOT NULL,
  PRIMARY KEY(attempt_id,sequence)
) STRICT;
CREATE TABLE operation_events (
  namespace TEXT NOT NULL, operation_id TEXT NOT NULL, sequence INTEGER NOT NULL CHECK(sequence>=1),
  event_type TEXT NOT NULL CHECK(event_type IN('accepted','attempt_prepared','dispatch_authorized',
    'output_stored','cancel_requested','attempt_settled','operation_terminal','reconciled')),
  payload_json TEXT NOT NULL CHECK(json_valid(payload_json)), created_at INTEGER NOT NULL,
  PRIMARY KEY(namespace,operation_id,sequence),
  FOREIGN KEY(namespace,operation_id) REFERENCES operations(namespace,operation_id)
) STRICT;
CREATE TRIGGER operation_events_no_update BEFORE UPDATE ON operation_events
BEGIN SELECT RAISE(ABORT,'operation events are append-only'); END;
CREATE TRIGGER operation_events_no_delete BEFORE DELETE ON operation_events
BEGIN SELECT RAISE(ABORT,'operation events are append-only'); END;
CREATE TABLE reconciliations (
  reconciliation_id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL REFERENCES attempts(attempt_id),
  principal_id TEXT NOT NULL, report_hash TEXT NOT NULL,
  evidence_json TEXT NOT NULL CHECK(json_valid(evidence_json)), correction_json TEXT NOT NULL CHECK(json_valid(correction_json)),
  created_at INTEGER NOT NULL
) STRICT;
CREATE TABLE audit_log (
  audit_id TEXT PRIMARY KEY, principal_id TEXT NOT NULL, namespace TEXT NOT NULL,
  action TEXT NOT NULL, target_ref TEXT NOT NULL, evidence_ref TEXT, created_at INTEGER NOT NULL
) STRICT;
CREATE TRIGGER operations_no_terminal_change BEFORE UPDATE OF state ON operations
WHEN OLD.state<>'running' AND NEW.state<>OLD.state
BEGIN SELECT RAISE(ABORT,'terminal operation is immutable'); END;
CREATE TRIGGER operations_no_terminal_result_change BEFORE UPDATE OF result_json ON operations
WHEN OLD.state<>'running' AND NEW.result_json IS NOT OLD.result_json
BEGIN SELECT RAISE(ABORT,'terminal result is immutable'); END;
CREATE TRIGGER events_no_update BEFORE UPDATE ON resource_events
BEGIN SELECT RAISE(ABORT,'resource events are append-only'); END;
CREATE TRIGGER events_no_delete BEFORE DELETE ON resource_events
BEGIN SELECT RAISE(ABORT,'resource events are append-only'); END;
