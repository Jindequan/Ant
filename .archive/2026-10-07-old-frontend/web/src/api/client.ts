/**
 * Shared API client for the ANT server (K6): cookie session, JSON encoding,
 * uniform error normalization to the ErrorEnvelope shape, and an explicit
 * no-auto-retry policy (429 and business submissions are never re-sent by the
 * client — callers decide what to do with the envelope). Session mutations
 * pass their CSRF token from memory; authentication state is never stored in
 * browser storage.
 */
import type { ErrorEnvelope } from './types';

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Resolve the API base URL: explicit argument, then VITE_API_BASE, then same-origin (''). */
export function resolveBaseUrl(explicit?: string): string {
  const fromEnv = import.meta.env['VITE_API_BASE'];
  const base =
    explicit ?? (typeof fromEnv === 'string' && fromEnv.length > 0 ? fromEnv : '');
  return base.replace(/\/+$/, '');
}

/** Normalized API failure carrying the contract ErrorEnvelope. */
export class ApiError extends Error {
  override readonly name = 'ApiError';
  readonly status: number;
  readonly envelope: ErrorEnvelope;

  constructor(status: number, envelope: ErrorEnvelope) {
    super(`API ${status} ${envelope.error.code} (requestId=${envelope.requestId})`);
    this.status = status;
    this.envelope = envelope;
  }
}

/** Envelope used when the payload does not match the error contract. */
export function fallbackErrorEnvelope(): ErrorEnvelope {
  return {
    error: {
      code: 'Unknown',
      messageKey: 'ant.error.unknown',
      retryable: false,
      action: 'retry_or_query_by_id',
    },
    requestId: '',
  };
}

function stringRecord(value: unknown): Record<string, string> | undefined {
  if (!isRecord(value)) return undefined;
  const out: Record<string, string> = {};
  let valid = true;
  for (const [key, entry] of Object.entries(value)) {
    if (typeof entry !== 'string') {
      valid = false;
      break;
    }
    out[key] = entry;
  }
  return valid ? out : undefined;
}

/**
 * Coerce an arbitrary error payload into the uniform ErrorEnvelope shape.
 * Unknown payloads never throw; they degrade to the fallback envelope so
 * callers always have code/messageKey/action to render or log.
 */
export function normalizeErrorEnvelope(payload: unknown): ErrorEnvelope {
  if (!isRecord(payload) || !isRecord(payload['error'])) {
    return fallbackErrorEnvelope();
  }
  const error = payload['error'];
  const code = error['code'];
  const messageKey = error['messageKey'];
  if (typeof code !== 'string' || typeof messageKey !== 'string') {
    return fallbackErrorEnvelope();
  }
  const fieldIssues = Array.isArray(error['fieldIssues'])
    ? error['fieldIssues'].filter(
        (issue): issue is { path: string; messageKey: string } =>
          isRecord(issue) &&
          typeof issue['path'] === 'string' &&
          typeof issue['messageKey'] === 'string',
      )
    : undefined;
  const operationId = error['operationId'];
  return {
    error: {
      code,
      messageKey,
      params: stringRecord(error['params']),
      fieldIssues: fieldIssues !== undefined && fieldIssues.length > 0 ? fieldIssues : undefined,
      operationId: typeof operationId === 'string' ? operationId : undefined,
      retryable: error['retryable'] === true,
      action: typeof error['action'] === 'string' ? error['action'] : '',
    },
    requestId: typeof payload['requestId'] === 'string' ? payload['requestId'] : '',
  };
}

export interface ApiRequestInit {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  /** JSON body; serialized when present. */
  body?: unknown;
  /** Overrides the resolved base URL for this call. */
  baseUrl?: string;
  /** CSRF token from the session projection, attached as a header when present. */
  csrfToken?: string;
  headers?: Record<string, string>;
  signal?: AbortSignal;
}

/**
 * Perform one API call. Throws {@link ApiError} for non-2xx responses with
 * the normalized envelope. There is deliberately no retry loop: rate limits
 * (429) and failed submissions surface to the caller untouched.
 *
 * The generic is the caller's declared wire type; payload-level validation is
 * a separate concern (`validate.ts` / per-endpoint guards when wired).
 */
export async function apiJson<T>(path: string, init: ApiRequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.body !== undefined) {
    headers.set('Content-Type', 'application/json');
  }
  if (init.csrfToken !== undefined && init.csrfToken.length > 0) {
    headers.set('X-CSRF-Token', init.csrfToken);
  }

  const response = await fetch(resolveBaseUrl(init.baseUrl) + path, {
    method: init.method ?? 'GET',
    credentials: 'include',
    headers,
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
    signal: init.signal,
  });

  const text = await response.text();
  let payload: unknown;
  if (text.length > 0) {
    try {
      payload = JSON.parse(text) as unknown;
    } catch {
      if (response.ok) {
        throw new ApiError(response.status, {
          error: { ...fallbackErrorEnvelope().error, code: 'InvalidJson' },
          requestId: '',
        });
      }
      payload = undefined;
    }
  }

  if (!response.ok) {
    throw new ApiError(response.status, normalizeErrorEnvelope(payload));
  }
  // 204 / empty body endpoints (e.g. DELETE /v1/session) resolve to undefined.
  return (payload === undefined ? undefined : payload) as T;
}
