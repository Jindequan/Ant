/**
 * SSE subscription primitive for persisted operation events
 * (K6 GET /v1/operations/{id}/events).
 *
 * Wire semantics (frozen contract): the `id:` line carries the sequence, the
 * `event:` line carries the OperationEventType, and `data:` carries the
 * OperationEvent JSON envelope.
 *
 * ── Resume-cursor contract (R15) ───────────────────────────────────────────
 * - The sequence is the resume cursor. Only events with a sequence strictly
 *   greater than the last delivered one reach `onEvent` (dedup/out-of-order
 *   guard). When both the `id:` line and the envelope carry a sequence, the
 *   `id:` line is authoritative.
 * - `initialLastSequence` seeds the FIRST resume position after a page
 *   rebuild: it is appended to the endpoint URL as `afterSequence=<n>`
 *   (prefixed with `?` or `&` depending on the endpoint). Browsers cannot set
 *   the `Last-Event-ID` header on a freshly constructed EventSource, so the
 *   first resume position travels in the URL; the server must apply the same
 *   semantics as `Last-Event-ID` ("deliver only sequences > n"). Automatic
 *   in-process reconnects keep using the browser-managed `Last-Event-ID`
 *   replayed from the `id:` lines.
 * - Scope note: this batch implements only the parameter and the URL
 *   assembly. Server-side cursor persistence, replay from storage and real
 *   disconnect/reconnect acceptance are out of scope here and are accepted in
 *   T2.4 (real SSE storage/reconnect).
 *
 * ── Malformed-event contract (R15) ────────────────────────────────────────
 * A delivery whose `data` is not JSON, or from which neither the `id:` line
 * nor the envelope yields a usable sequence, is ignored and counted
 * (`getMalformedCount()`). The client never guesses a cursor position from a
 * malformed delivery. Fallback: the UI reconciles by querying the operation
 * by its ORIGINAL id (GET /v1/operations/{id}; error action
 * `retry_or_query_by_id`) instead of resuming from an unknown offset. The
 * subscription stays open on malformed deliveries — the count is the
 * observable the UI uses to decide to fall back. Retention/backfill gaps
 * (server already dropped old sequences) surface via `onError`; the same
 * query-by-id fallback applies there.
 */
import { resolveBaseUrl } from './client';

/**
 * Local structural mirror of the wire `OperationEvent` (contracts/openapi.yaml
 * OperationEvent / OperationEventType). Declared here instead of imported from
 * `./types` for T1-rework parallel-isolation: types.ts is being rewritten
 * concurrently and this module must only depend on DOM/EventSource types.
 * Structural identity is guaranteed by the shared frozen contract; the
 * duplicate is collapsed into a re-export from types.ts at the integration
 * barrier once the frozen surface lands.
 */
export type OperationEventType =
  | 'accepted'
  | 'attempt_prepared'
  | 'dispatch_authorized'
  | 'output_stored'
  | 'cancel_requested'
  | 'attempt_settled'
  | 'operation_terminal'
  | 'reconciled';

/** Structural mirror of the wire OperationEvent (see type-isolation note above). */
export interface OperationEvent {
  operationId: string;
  sequence: number;
  type: OperationEventType;
  createdAt: number;
  payload: Record<string, unknown>;
}

export const OPERATION_EVENT_TYPES: readonly OperationEventType[] = [
  'accepted',
  'attempt_prepared',
  'dispatch_authorized',
  'output_stored',
  'cancel_requested',
  'attempt_settled',
  'operation_terminal',
  'reconciled',
];

export interface SubscribeOperationEventsOptions {
  /**
   * App-relative or absolute http(s) path of the SSE endpoint, e.g. the
   * eventsUrl from a SubmitReply. Absolute endpoints are used verbatim; see
   * {@link resolveEventsUrl}.
   */
  url: string;
  /**
   * First resume position: appended to the URL as `afterSequence=<n>` and used
   * as the dedup floor (only strictly greater sequences are delivered).
   * Non-positive/invalid values are treated as "no cursor".
   */
  initialLastSequence?: number;
  onEvent: (event: OperationEvent) => void;
  onError?: (error: unknown) => void;
  /** Aborting the signal closes the subscription and removes the listener. */
  signal?: AbortSignal;
}

export interface OperationEventSubscription {
  /** Idempotent; safe to call multiple times and after abort. */
  close(): void;
  /** Last sequence delivered to onEvent, or the initial cursor if none. */
  getLastSequence(): number;
  /** Deliveries ignored because no usable sequence/envelope could be read. */
  getMalformedCount(): number;
}

const URL_SCHEME = /^[a-z][a-z0-9+.-]*:/i;
const HTTP_SCHEME = /^https?:\/\//i;

/**
 * Resolve the EventSource URL from an API base and an endpoint.
 *
 * Contract (R15):
 * - Absolute http(s) endpoints are used verbatim — never joined with the base.
 * - Relative endpoints are joined with the base, handling slash boundaries on
 *   both sides. Without a base the endpoint stays a same-origin relative path.
 * - Only http/https are supported: any other scheme (`ws:`, `wss:`,
 *   `javascript:`, `data:` …) on either side throws; protocol-relative
 *   (`//host/...`) endpoints are rejected because the effective scheme could
 *   not be verified.
 */
export function resolveEventsUrl(base: string, endpoint: string): string {
  if (endpoint.startsWith('//')) {
    throw new Error(`Events endpoint must be absolute http(s) or relative, not protocol-relative: ${endpoint}`);
  }
  if (URL_SCHEME.test(endpoint)) {
    if (!HTTP_SCHEME.test(endpoint)) {
      throw new Error(`Events endpoint scheme must be http/https: ${endpoint}`);
    }
    return endpoint;
  }
  if (URL_SCHEME.test(base) && !HTTP_SCHEME.test(base)) {
    throw new Error(`Events base URL scheme must be http/https: ${base}`);
  }
  return base.replace(/\/+$/, '') + (endpoint.startsWith('/') ? endpoint : `/${endpoint}`);
}

function normalizeCursor(value: number | undefined): number {
  if (value === undefined) return 0;
  const floored = Math.floor(value);
  return Number.isFinite(floored) && floored > 0 ? floored : 0;
}

/** Append the first-resume cursor to the endpoint URL (`afterSequence=<n>`). */
function withResumeCursor(url: string, afterSequence: number): string {
  if (afterSequence <= 0) return url;
  return `${url}${url.includes('?') ? '&' : '?'}afterSequence=${afterSequence}`;
}

function isOperationEventType(value: unknown): value is OperationEventType {
  return typeof value === 'string' && (OPERATION_EVENT_TYPES as readonly string[]).includes(value);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isOperationEvent(value: unknown): value is OperationEvent {
  return (
    isRecord(value) &&
    typeof value['operationId'] === 'string' &&
    typeof value['sequence'] === 'number' &&
    isOperationEventType(value['type']) &&
    typeof value['createdAt'] === 'number' &&
    isRecord(value['payload'])
  );
}

function parseEvent(data: string): OperationEvent | undefined {
  try {
    const parsed: unknown = JSON.parse(data);
    return isOperationEvent(parsed) ? parsed : undefined;
  } catch {
    return undefined;
  }
}

export function subscribeOperationEvents(
  options: SubscribeOperationEventsOptions,
): OperationEventSubscription {
  const signal = options.signal;
  let lastSequence = normalizeCursor(options.initialLastSequence);
  let malformedCount = 0;
  let closed = false;
  let source: EventSource | undefined;

  const close = (): void => {
    if (closed) return;
    closed = true;
    // Remove the abort listener first so a torn-down subscription keeps no
    // reference to the caller's signal alive (R15: no listener leak).
    signal?.removeEventListener('abort', close);
    source?.close();
    source = undefined;
  };

  const subscription: OperationEventSubscription = {
    close,
    getLastSequence: () => lastSequence,
    getMalformedCount: () => malformedCount,
  };

  // An already-aborted signal must leave no active connection: build nothing.
  if (signal?.aborted) {
    return subscription;
  }

  const handleData = (data: string, lastEventId: string): void => {
    const event = parseEvent(data);
    // The id: line is authoritative for the cursor; the envelope sequence is
    // the fallback when the server sent no id.
    const idSequence = Number.parseInt(lastEventId, 10);
    const sequence = Number.isFinite(idSequence) ? idSequence : event?.sequence;
    if (event === undefined || sequence === undefined || !Number.isFinite(sequence)) {
      // Malformed delivery: ignore and count; the UI falls back to querying
      // the operation by its original id (see module contract comment).
      malformedCount += 1;
      return;
    }
    if (sequence <= lastSequence) return; // replay / out-of-order: dropped, not malformed
    lastSequence = sequence;
    options.onEvent(event);
  };

  const url = withResumeCursor(resolveEventsUrl(resolveBaseUrl(), options.url), lastSequence);
  source = new EventSource(url);

  for (const type of OPERATION_EVENT_TYPES) {
    source.addEventListener(type, (raw) => {
      const message = raw as MessageEvent<string>;
      handleData(message.data, message.lastEventId);
    });
  }

  source.onmessage = (message) => {
    handleData(message.data, message.lastEventId);
  };

  source.onerror = (error) => {
    // EventSource reconnects on its own, resending Last-Event-ID. Surfaced so
    // the UI can show a "reconnect / query by original id" state (T5).
    options.onError?.(error);
  };

  signal?.addEventListener('abort', close, { once: true });

  return subscription;
}
