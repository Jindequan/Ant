/**
 * Wire/contract type surface for the ANT web console (K6, R05).
 *
 * Single consumption path: every wire type here is DERIVED from generated
 * sources — there are no hand-written field mirrors left in this module.
 *
 * - `schema.d.ts` is generated from contracts/openapi.yaml
 *   (v1.1.20) by `npm run generate:types` (openapi-typescript).
 * - `config-document.gen.d.ts` is generated from
 *   contracts/schema/config-document.schema.json by
 *   `npm run generate:config-types` (json-schema-to-typescript).
 * - `npm run guard:gen` regenerates both into memory and fails non-zero on
 *   drift, so a contract change without regeneration breaks CI instead of
 *   silently diverging.
 *
 * Contract rules that stay encoded here as documentation:
 * - Quantities are exact decimal integer strings over int64 minimal units
 *   and must never be modelled as `number` (openapi `Quantity`, config
 *   schema `$defs/quantity`); the runtime BigInt codec lives in validate.ts.
 * - `Millis` fields are int64 epoch milliseconds (JSON number).
 * - The SubmitReply oneOf in openapi generates one object type per variant
 *   with a literal `kind`, so the generated union is directly discriminable
 *   by `kind` — no separate discriminated mirror is written by hand.
 */

import type { components } from './schema';
import type { ANTConfigDocument } from './config-document.gen';

type schemas = components['schemas'];

// ---------------------------------------------------------------------------
// Shared scalars and error/session DTOs (openapi components/schemas)
// ---------------------------------------------------------------------------

export type ID = schemas['ID'];
/** Exact decimal integer over int64 minimal units; no floats, no exponents. */
export type Quantity = schemas['Quantity'];
/** Epoch milliseconds (int64). */
export type Millis = schemas['Millis'];

export type FieldIssue = schemas['FieldIssue'];
export type ErrorDTO = schemas['ErrorDTO'];
export type ErrorEnvelope = schemas['ErrorEnvelope'];
export type SessionView = schemas['SessionView'];
export type SessionPermission = SessionView['permissions'][number];

// ---------------------------------------------------------------------------
// ConfigDocument (K1) — generated from contracts/schema/config-document.schema.json
// ---------------------------------------------------------------------------

export type {
  ANTConfigDocument,
  Account,
  Binding,
  Evidence,
  EntitlementGroup,
  Grant,
  Id,
  NoSecretObject,
  Offer,
  Pool,
  Resource,
  Unit,
  UnsupportedDraft,
  Window,
} from './config-document.gen';

/** Config document root; generated from the JSON Schema, not hand-written. */
export type ConfigDocument = ANTConfigDocument;

// ---------------------------------------------------------------------------
// Pools (openapi components/schemas)
// ---------------------------------------------------------------------------

export type PoolMember = schemas['PoolMember'];
export type ResourceOffer = schemas['ResourceOffer'];
export type ResourcePool = schemas['ResourcePool'];

/** Availability/evidence projection of one resource. Quantities are strings; `available` omitted means unknown, never 0-by-default. */
export type ResourceView = schemas['ResourceView'];

export type PoolMemberView = schemas['PoolMemberView'];
export type PoolMemberStatus = PoolMemberView['status'];
export type PoolGroup = schemas['PoolGroup'];
export type PoolView = schemas['PoolView'];

export type ReorderPoolEntry = schemas['ReorderPoolEntry'];
export type ReorderPoolInput = schemas['ReorderPoolInput'];

// ---------------------------------------------------------------------------
// Operations (openapi components/schemas)
// ---------------------------------------------------------------------------

export type Reason = schemas['Reason'];

export type ResourceOutcome = schemas['ResourceOutcome'];
export type ConsumptionBasis = ResourceOutcome['consumptionBasis'];
export type OccupancyState = ResourceOutcome['occupancy'];

export type OperationResult = schemas['OperationResult'];
export type RetrySafety = OperationResult['retrySafety'];
export type OutputAvailability = OperationResult['outputAvailability'];
export type TerminalOperationState = OperationResult['state'];

export type AttemptView = schemas['AttemptView'];
export type ReconciliationView = schemas['ReconciliationView'];
export type OperationView = schemas['OperationView'];
export type OperationState = OperationView['state'];

export type SubmitAccepted = schemas['SubmitAccepted'];
export type SubmitInProgress = schemas['SubmitInProgress'];
export type SubmitReplay = schemas['SubmitReplay'];
export type SubmitRejected = schemas['SubmitRejected'];

/**
 * SubmitReply, straight from the generated oneOf union (R05). Each variant
 * carries a literal `kind`, so TypeScript narrows this union on `kind`
 * without any additional discriminated mirror. The wire constraints that a
 * plain type cannot express (accepted ⇔ state="running" + URLs; replay
 * result terminal; top-level state consistency) are re-checked at runtime
 * by `validateSubmitReply` in validate.ts, mirroring
 * server internal/api ValidateSubmitReply.
 */
export type SubmitReply = schemas['SubmitReply'];

export type CancellationAck = schemas['CancellationAck'];
export type CancellationAckStatus = CancellationAck['status'];

export type OutputChunk = schemas['OutputChunk'];
export type OutputPage = schemas['OutputPage'];

export type OperationEvent = schemas['OperationEvent'];
export type OperationEventType = OperationEvent['type'];

export type DomainRequest = schemas['DomainRequest'];
export type ExecuteInput = schemas['ExecuteInput'];
export type OperationCancelInput = schemas['OperationCancelInput'];
