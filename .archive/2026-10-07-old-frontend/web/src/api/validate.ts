/**
 * Runtime contract validation boundary for the Web console (K1 client side,
 * K6 codec guard). TS types in `types.ts` disappear at runtime; this module
 * provides the actual checks.
 *
 * Layering (R02): JSON Schema shape (ajv, below) → typed reference-graph
 * semantics (config-semantics.ts, mirror of internal/config/validate_graph.go)
 * → optional provider-catalog checks (R12, config-catalog.ts, mirror of
 * internal/config validateCatalog). Issue messageKeys equal the Go keys
 * ("ant.config.*") so manifest-driven tests assert cross-language parity.
 *
 * Entry points:
 * - validateConfigDocument(json): schema + semantics. Mirrors the shared
 *   probe entry (Go: strict decode + ValidateStructural, no catalog).
 * - validateConfigDocument(json, { catalog }): adds the registered-directory
 *   phase (Q1: context schemas, resolution identity — every resource resolves
 *   to exactly one verified fact instance whose dimensions/capacity bound it,
 *   semantics registry, profile bundles); { catalog: null } fails closed with
 *   "catalogUnavailable" like Go Validate(data, nil); omitting the option
 *   keeps the catalog-free entry (resolution skipped). Zero issues yield the
 *   compiled execution summary (Go DocumentValidation.Compiled).
 * - validateConfigDocumentSemantics(json): semantics without the schema
 *   gate, for per-layer manifest assertions.
 * - validateSubmitReply / validateOperationResult / validateIfMatch: wire
 *   codec guards mirroring internal/api validate.go.
 *
 * The server stays authoritative: saving broken drafts is allowed, a
 * non-empty issue list only means "not validated/activated".
 */
import Ajv2020 from 'ajv/dist/2020';
import addFormats from 'ajv-formats';
import rawConfigSchema from '../../../contracts/schema/config-document.schema.json';
import type { ConfigIssue, Entity } from './config-helpers';
import { obj } from './config-helpers';
import { runStructural } from './config-semantics';
import type { CompiledConfigSummary, ProviderCatalog } from './config-catalog';
import { buildCompiledConfig, validateCatalogPhase } from './config-catalog';
import type { TerminalOperationState } from './types';

export type { ConfigIssue } from './config-helpers';
export { parseQuantity, windowsExecutableEqual, factWindowRule, windowRuleOf } from './config-helpers';
export { validateConfigDocumentSemantics } from './config-semantics';
export type {
  CompiledConfigSummary,
  CompiledOfferSummary,
  CompiledResourceSummary,
  EntitlementFact,
  ExtensionContext,
  ProviderCatalog,
  ProviderDescriptorInput,
  RegisteredProfileInput,
  RegisteredSemanticsInput,
  VerifiedResourceFact,
  VerifiedScopeInput,
} from './config-catalog';
export { createDescriptorCatalog, validateCatalogPhase } from './config-catalog';
/**
 * Zero issues + catalog phase yield the consumable execution summary (Q1):
 * resources carry their RESOLVED physical ledger keys, quantity space and
 * verified capacity; offers the physical keys of their bundles. Compiled is
 * present exactly when the document is ok — structural validity alone never
 * promises executability (mirror of Go DocumentValidation.Compiled).
 */
export type ValidationResult = { ok: true; compiled?: CompiledConfigSummary } | { ok: false; issues: ConfigIssue[] };

// The JSON import is structurally the schema document; ajv itself re-validates
// the schema when compiling and throws on a malformed one, so this boundary
// cast is safe.
const configSchema = rawConfigSchema as unknown as object;

function buildValidator(schema: object) {
  const ajv = new Ajv2020({ allErrors: true });
  addFormats(ajv);
  return ajv.compile(schema);
}

const validateSchema = buildValidator(configSchema);

/**
 * Validate an untrusted JSON value against the ConfigDocument contract:
 * JSON Schema first, then cross-reference semantics (+ optional registered
 * catalog phase; { catalog: null } fails closed). With a catalog, every
 * resource must RESOLVE to exactly one verified fact instance whose
 * dimensions and verified capacity constrain the configuration (Q1); zero
 * issues additionally yield the compiled execution summary. Schema error
 * paths are kept in the issues so callers can point at the offending field.
 */
export function validateConfigDocument(
  json: unknown,
  opts?: { catalog?: ProviderCatalog | null },
): ValidationResult {
  if (!validateSchema(json)) {
    const issues = (validateSchema.errors ?? []).map((error) => ({
      path: error.instancePath || '/',
      messageKey: 'ant.config.schemaViolation',
    }));
    return { ok: false, issues };
  }
  const doc = obj(json) as Entity;
  const issues = runStructural(doc);
  if (opts?.catalog !== undefined) {
    const phase = validateCatalogPhase(doc, opts.catalog);
    issues.push(...phase.issues);
    if (issues.length > 0) return { ok: false, issues };
    return { ok: true, compiled: buildCompiledConfig(doc, phase.resolutions) };
  }
  return issues.length > 0 ? { ok: false, issues } : { ok: true };
}

// ---------------------------------------------------------------------------
// SubmitReply / result / If-Match codec guards (K6) — mirrors of internal/api
// validate.go, exercised per fixture by the contract tests.
// ---------------------------------------------------------------------------

const TERMINAL_STATES: readonly TerminalOperationState[] = ['succeeded', 'failed', 'cancelled', 'unknown'];

function isTerminalState(value: unknown): value is TerminalOperationState {
  return typeof value === 'string' && (TERMINAL_STATES as readonly string[]).includes(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function requireNonEmptyString(record: Record<string, unknown>, field: string, issues: ConfigIssue[]): void {
  const value = record[field];
  if (typeof value !== 'string' || value.length === 0) {
    issues.push({ path: `/${field}`, messageKey: 'ant.error.fieldRequired' });
  }
}

/**
 * Runtime guard for SubmitReply (the TS union is erased at runtime, and the
 * server could regress). Mirrors internal/api ValidateSubmitReply:
 * accepted ⇒ state "running" + operationUrl/eventsUrl, nothing else;
 * inProgress ⇒ running operation, optional top state must be "running";
 * replay ⇒ result with terminal state, top state must match it;
 * rejected ⇒ error and nothing else; unknown kind ⇒ codec error.
 */
export function validateSubmitReply(json: unknown): ValidationResult {
  if (!isRecord(json)) {
    return { ok: false, issues: [{ path: '/', messageKey: 'ant.error.illegalSubmitVariant' }] };
  }
  const issues: ConfigIssue[] = [];
  requireNonEmptyString(json, 'operationId', issues);
  switch (json.kind) {
    case 'accepted': {
      if (json.state !== 'running') {
        issues.push({ path: '/state', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      requireNonEmptyString(json, 'operationUrl', issues);
      requireNonEmptyString(json, 'eventsUrl', issues);
      if (isRecord(json.operation) || isRecord(json.result) || isRecord(json.error)) {
        issues.push({ path: '/', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      break;
    }
    case 'inProgress': {
      if (!isRecord(json.operation)) {
        issues.push({ path: '/operation', messageKey: 'ant.error.illegalSubmitVariant' });
      } else if (json.operation['state'] !== 'running') {
        issues.push({ path: '/operation/state', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      if (json.state !== undefined && json.state !== 'running') {
        issues.push({ path: '/state', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      break;
    }
    case 'replay': {
      if (!isRecord(json.result)) {
        issues.push({ path: '/result', messageKey: 'ant.error.illegalSubmitVariant' });
        break;
      }
      if (!isTerminalState(json.result['state'])) {
        issues.push({ path: '/result/state', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      if (json.state !== undefined && json.state !== json.result['state']) {
        issues.push({ path: '/state', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      break;
    }
    case 'rejected': {
      if (!isRecord(json.error)) {
        issues.push({ path: '/error', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      if (isRecord(json.operation) || isRecord(json.result)) {
        issues.push({ path: '/', messageKey: 'ant.error.illegalSubmitVariant' });
      }
      break;
    }
    default:
      issues.push({ path: '/kind', messageKey: 'ant.error.illegalSubmitVariant' });
  }
  return issues.length > 0 ? { ok: false, issues } : { ok: true };
}

/** Mirror of internal/api ValidateOperationResult: results are terminal. */
export function validateOperationResult(json: unknown): ValidationResult {
  if (!isRecord(json)) {
    return { ok: false, issues: [{ path: '/', messageKey: 'ant.error.illegalResultState' }] };
  }
  if (isTerminalState(json.state)) return { ok: true };
  return { ok: false, issues: [{ path: '/state', messageKey: 'ant.error.illegalResultState' }] };
}

/**
 * Mirror of internal/api ValidateIfMatch: the If-Match header must equal the
 * body-borne expected version; conflict is an explicit rejection, never a
 * silent override by either value.
 */
export function validateIfMatch(headerValue: string, expectedVersion: number): ValidationResult {
  let hv = headerValue.trim();
  const invalid = (): ValidationResult => ({
    ok: false,
    issues: [{ path: '/', messageKey: 'ant.error.versionHeaderMismatch' }],
  });
  if (hv === '' || expectedVersion < 0 || hv.startsWith('W/') || hv.includes(',') || hv === '*') {
    return invalid();
  }
  if (hv.startsWith('"') || hv.endsWith('"')) {
    if (hv.length < 2 || !hv.startsWith('"') || !hv.endsWith('"')) return invalid();
    hv = hv.slice(1, -1);
  }
  if (!/^(0|[1-9]\d*)$/.test(hv) || !Number.isSafeInteger(Number(hv))) {
    return invalid();
  }
  if (Number(hv) !== expectedVersion) {
    return invalid();
  }
  return { ok: true };
}
