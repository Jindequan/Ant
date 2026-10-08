# HTTP API, SSE, and the Duplex Boundary

Version: V0.7. Go DTOs in [contracts.go](contracts.go), pages in the [Web contract](09-WEB-CONSOLE.md). The API is a mapping of the server's business application ports; handlers do not implement a second dispatch system.

## 1. Authentication and encoding

First-version login input is a high-entropy admin token issued by the CLI (not a self-chosen weak password), which is exchanged for a session after hash verification; clients do not persist the token to localStorage. Same-origin Web uses a server-side opaque session cookie (HttpOnly, SameSite=Strict, Secure under HTTPS) plus CSRF token/Origin validation; the initial administrator is bootstrapped once via a local-only bootstrap CLI, with no default admin password and no unauthenticated public access. Business programs use high-entropy API tokens; the server stores only their hash, bound to namespace/permissions; the request body cannot declare identity authorization. Sessions can be revoked on logout/expiry; SSE re-verifies permissions on each subscription and periodically.

All /API business and configuration entry points require authentication; health returns only health and leaks no configuration. Secret endpoints are write-only/rotate/masked-read; there is no plaintext read. Request body limit starts at 1MiB; attachments accept only verified immutableRef; limits are frozen in T1. JSON quantities use decimal strings, times use epoch-ms, unknown values are omitted or carry explicit availability, and numeric 0 must not substitute for unknown.

Unified error: `{error:{code,messageKey,params,fieldIssues,operationId?,retryable,action},requestId}`. 400 field errors, 401 session invalid, 403 permissions, 404 invisible/nonexistent, 409 identity or version conflict, 422 configuration not executable, 429 Busy, 503 storage/host unavailable; business failed/unknown are normal Operation projections, not wrapped as 500. Schema parsing rejects unknown business fields and illegal enums; unknown provider draft fields must land inside the extension object allowed by the descriptor schema.

## 2. Routes and concrete responsibilities

| Method/path (all /v1) | Go handler → application command | Returns / key guards |
|---|---|---|
| POST /sessions, GET /session, DELETE /session | SessionService.Login/Current/Logout | CSRF/rate limit; logout closes subscriptions and does not revoke already-made outbound-call facts |
| GET /catalog/providers | Catalog.List | Adapter types, domains, form schemas, support/evidence conditions, no secrets |
| GET /config, GET /config/revisions | ConfigService.Read/History | Current complete projection and versions; paginated history |
| POST /config/drafts, GET /config/drafts/{id} | SaveDraft/GetDraft | commandId/draftVersion; erroneous drafts can be saved |
| PATCH /config/drafts/{id} | UpdateDraft | If-Match; updates drafts only |
| POST /config/drafts/{id}/validate | ValidateDraft | fieldIssues/evidence expiry; network outside the transaction |
| POST /config/drafts/{id}/activate | ActivateDraft | expectedConfigRevision + commandId; atomic publish |
| POST /secrets, POST /secrets/{id}/rotate, GET /secrets/{id} | SecretCommands.Create/Rotate/Get | configure permission; principal+namespace ownership; ciphertext and idempotent receipt in the same transaction; returns only ref/revision/mask |
| POST /providers/deepseek/balance-observations, GET /providers/deepseek/balance-observations/{commandId} | DeepSeekBalanceVerification.Observe/Get | configure permission; parses only the caller's own current credential; one read-only balance request; receipt and audit in the same transaction; reads by command ID and does not promote the balance into a hard quota |
| GET /providers, GET /resources, GET /offers | QueryAdmin.ListConfigEntities | Current enabled/blocked/evidence/pagination; entity writes go through drafts |
| GET /pools, GET /pools/{id} | QueryAdmin.Pools/Pool | Grouped by capability/unit, no mixed total balance |
| PUT /pools/{id}/order | ConfigService.ReorderPool | If-Match + expectedConfigRevision + commandId; total order saved atomically |
| POST /pools/{id}/operations | Router.Submit | ExecuteInput; poolId path bound to the fingerprint; 202 newly accepted, 200 existing projection, 409 conflict |
| GET /operations, GET /operations/{id} | QueryAdmin.List/GetOperation | namespace taken from identity; the list can optionally filter by persisted state, cursor bound to state, detail reads the same OperationView |
| POST /operations/{id}/cancel | Ledger.RequestCancel | commandId; 200 is only an ACK of stop intent, the result is still queried |
| GET /operations/{id}/events | EventService.Subscribe | SSE; afterSequence or Last-Event-ID; read permission |
| GET /operations/{id}/output | QueryAdmin.ReadOutput | After namespace authorization, reads committed blocks by sequence; defaults afterSequence=0, limit=50 (max 100); the response includes complete and availability, and when there are no blocks in a running operation availability=none and complete=false |
| GET /operations/{id}/decisions | QueryAdmin.Decisions | Persisted selections/priorities/versions, not recomputed |
| POST /resources/{id}/observations | QueryAdmin.RefreshObservation | manage + source/commandId; only stored as a valid observation when within the same-resource window, the instant is unexpired, and it matches the committed watermark and adjustment version; watermark lag is conservatively deducted by the concurrent consumption difference; business resending is not allowed |
| POST /resources/{id}/adjustments | Ledger.Adjust | manage + evidence, linked consumption, commandId; appends an adjustment event, changes adjustmentVersion, and invalidates old observations; negative corrections must link consumption events still available for offset; genuine over-capacity adjustments are recorded truthfully and establish a resource hold |
| POST /attempts/{id}/reconciliations | Ledger.Reconcile | manage + evidence, commandId, confirmed result, and per-resource corrections; receipts are replayable; only appends verification/ledger events, never changes the original terminal state; does not release uncertain reservations when the basis for completion is unclear |
| POST /holds/{id}/clear | Ledger.ClearHold | manage + completion/release basis; clears only open holds visible to the calling namespace; conflict when linked open encumbrances exist, unknown reservations are never released |

T1 generates the OpenAPI spec and Web types from this table and the Go DTOs; common-field tests must check paths, status codes, unit strings, versions, and optional fields. OpenAPI is a network projection and must not expose internal permit/SecretResolver. Every configuration object goes through the same draft mechanism for create/update/delete; the first version does not simultaneously provide multiple activation entry points that bypass each other.

## 3. Admission and run lifecycle

POST first authenticates/validates/claims → ExecutionHost takes over with a bound → 202 `{kind:"accepted",operationId,state:"running",operationUrl,eventsUrl}`. Only when the takeover holds is accepted promised; server crashes converge via the lease. If the client loses the response, it keeps the operationId and re-queries or resubmits with the same ID; it does not invent a new ID and resend.

The internal run handle may have a read-only done channel and a notification channel; HTTP does not pass them. The execution context derives from the server supervisor and applies a deadline; reusing r.Context is not allowed, otherwise a browser disconnect would interrupt the ledger lifecycle. Cancellation is a persistent command, then the host is notified; the independent deadline-bounded settlement context does not lose its chance to settle because a provider read finds the ctx already canceled.

## 4. Event protocol and reconnection

operation_events are durably ordered by namespace/operation/sequence. Event envelope: `{operationId,sequence,type,createdAt,payload}`; types accepted, attempt_prepared, dispatch_authorized, output_stored, cancel_requested, attempt_settled, operation_terminal, reconciled. output_stored carries only the outputRef/chunkSequence range; the body is read via the authorized output endpoint, and large bodies are not duplicated into events.

State writes and event writes share one transaction; after commit the EventHub notifies non-blockingly. The hub only provides wake-ups and does not guarantee reliable delivery; a subscriber first queries the DB, registers, then queries the DB again to avoid missing events between query and registration, with periodic catch-up queries as a fallback. A new subscription is a read of persisted events/outputs; it does not restore old Promises or the original network stream, and does not re-call providers.

SSE `id`=sequence, `event`=type, `data`=JSON, with a comment heartbeat every 15s; every event write has a deadline, and the server's global WriteTimeout must not truncate legitimate long streams. Per-subscription buffer 128 items/128KiB and 8 subscriptions per user are starting values; slow consumers are disconnected and may catch up by reading in order, without blocking execution or caching without bound. Out-of-range sequence numbers return 400; when the requested body has been cleaned up, the response returns availability/the gap and cannot quietly fill it in. After the terminal event is sent, the stream closes and the UI queries the final projection; duplicate sequence numbers are deduplicated by the frontend.

Body output still follows persist-before-visible; each chunk of at most 32KiB is committed in one transaction together with SHA-256, the first segment's visible_output_at, and the `output_stored` sequence-range event, and only then becomes visible to queries. Per-operation durable body cap is 16MiB; chunk numbers increment from 1, an exact replay is idempotent, and conflicts and skipped numbers are rejected. Paginated content returns only committed bytes, base64-encoded in the response. A closed stream does not mean the business has ended; once the first segment is visible, automatically switching providers to stitch output is not allowed. Queries/SSE reconnections re-verify authorization; the terminal display may independently keep unknown reservations.

## 5. Why the first version does not mandate WebSocket

Configuration, submission, and cancellation go over HTTP; the server pushes status/text to the Web over SSE, and cancellation can be submitted while a subscription is open. Protocol basis: [WHATWG SSE](https://html.spec.whatwg.org/multipage/server-sent-events.html).

Continuous audio/video input or interactive provider sessions need full duplex; a SessionGateway (WebSocket) can be added into the application ports; basis: [RFC6455](https://www.rfc-editor.org/rfc/rfc6455.html). That requires separately defining input sequence numbers, ACKs, rates, session bounds, incremental reservations, completion evidence, and recovery, and not squeezing unbounded sessions into a single HTTP attempt. WebSocket is not a prerequisite of the current text/email configuration and dispatch closed loop; when a provider session has not been admitted, it is rejected explicitly.
