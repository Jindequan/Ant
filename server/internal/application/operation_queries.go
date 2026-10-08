package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
	"ant/server/internal/store"
)

var (
	ErrOperationQueryForbidden = errors.New("operation query: forbidden")
	ErrOperationQueryNotFound  = errors.New("operation query: not found")
	ErrOperationQueryInvalid   = errors.New("operation query: invalid input")
	ErrOperationQueryStore     = errors.New("operation query: store lacks read capability")
	ErrOperationQueryIntegrity = errors.New("operation query: persisted data integrity error")
)

const (
	DefaultOperationPageSize    = 50
	MaxOperationPageSize        = 100
	MaxOperationEventPageSize   = 100
	MaxOperationEventBytes      = 64 << 10
	MaxOperationDecisionRecords = 256
	MaxOperationDecisionBytes   = 64 << 10
)

type OperationPage struct {
	Operations []domain.OperationView `json:"operations"`
	NextCursor string                 `json:"nextCursor,omitempty"`
}

type OperationEventPage struct {
	Events       []domain.OperationEvent `json:"events"`
	LastSequence int64                   `json:"lastSequence"`
	State        domain.OperationState   `json:"state"`
}

type OperationQueries struct{ store store.Store }

func NewOperationQueries(st store.Store) (*OperationQueries, error) {
	if st == nil {
		return nil, ErrOperationQueryInvalid
	}
	return &OperationQueries{store: st}, nil
}

func authorizeOperationRead(auth domain.AuthContext) error {
	if auth.PrincipalID == "" || auth.Namespace == "" ||
		(!slices.Contains(auth.Permissions, domain.PermRead) && !slices.Contains(auth.Permissions, domain.PermConfigure)) {
		return ErrOperationQueryForbidden
	}
	return nil
}

func (q *OperationQueries) Get(ctx context.Context, auth domain.AuthContext, operationID domain.ID) (domain.OperationView, error) {
	if q == nil || authorizeOperationRead(auth) != nil {
		return domain.OperationView{}, ErrOperationQueryForbidden
	}
	if operationID == "" {
		return domain.OperationView{}, ErrOperationQueryInvalid
	}
	var view domain.OperationView
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		record, found, err := reader.GetOperation(auth.Namespace, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrOperationQueryNotFound
		}
		view, err = projectOperation(reader, record)
		if err != nil || !slices.Contains(auth.Permissions, domain.PermManage) {
			return err
		}
		admin, ok := rx.(store.AdminResourceReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		charges, err := admin.ListAdjustableChargesForOperation(auth.Namespace, operationID)
		if err != nil {
			return err
		}
		for _, charge := range charges {
			view.AdjustableCharges = append(view.AdjustableCharges, domain.AdjustableChargeView{
				EventID: charge.EventID, ResourceID: charge.ResourceID, WindowKey: charge.WindowKey,
				MetricID: charge.MetricID, Unit: charge.Unit, RemainingReversible: charge.RemainingReversible,
			})
		}
		seen := map[domain.ID]bool{}
		for _, outcome := range view.Resources {
			if outcome.ResourceID == "" || seen[outcome.ResourceID] {
				continue
			}
			seen[outcome.ResourceID] = true
			holds, err := admin.ListManagedHoldsForResource(auth.Namespace, outcome.ResourceID)
			if err != nil {
				return err
			}
			for _, hold := range holds {
				view.ManagedHolds = append(view.ManagedHolds, domain.ManagedHoldView{
					HoldID: hold.HoldID, ResourceID: outcome.ResourceID, ScopeKind: hold.ScopeKind,
					ScopeID: hold.ScopeID, Reason: hold.Reason, EvidenceRefs: hold.EvidenceRefs, CanClear: hold.CanClear,
				})
			}
		}
		return nil
	})
	return view, err
}

func (q *OperationQueries) List(ctx context.Context, auth domain.AuthContext, cursor string, limit int, state string) (OperationPage, error) {
	if q == nil || authorizeOperationRead(auth) != nil {
		return OperationPage{}, ErrOperationQueryForbidden
	}
	if limit == 0 {
		limit = DefaultOperationPageSize
	}
	if limit < 1 || limit > MaxOperationPageSize {
		return OperationPage{}, ErrOperationQueryInvalid
	}
	if !validOperationListState(state) {
		return OperationPage{}, ErrOperationQueryInvalid
	}
	beforeAt, beforeID, cursorState, err := decodeOperationCursor(cursor)
	if err != nil {
		return OperationPage{}, err
	}
	if cursor != "" && cursorState != state {
		return OperationPage{}, ErrOperationQueryInvalid
	}
	var page OperationPage
	err = q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		records, err := reader.ListOperations(auth.Namespace, state, beforeAt, beforeID, limit+1)
		if err != nil {
			return err
		}
		if len(records) > limit {
			records = records[:limit]
			last := records[len(records)-1]
			page.NextCursor = encodeOperationCursor(last.UpdatedAt, last.OperationID, state)
		}
		page.Operations = make([]domain.OperationView, 0, len(records))
		for _, record := range records {
			view, err := projectOperation(reader, record)
			if err != nil {
				return err
			}
			page.Operations = append(page.Operations, view)
		}
		return nil
	})
	return page, err
}

// Events reads only committed event rows. The watermark and rows share one
// read snapshot so callers can detect invalid future cursors without racing a
// concurrent append.
func (q *OperationQueries) Events(ctx context.Context, auth domain.AuthContext, operationID domain.ID, afterSequence int64, limit int) (OperationEventPage, error) {
	if q == nil || authorizeOperationRead(auth) != nil {
		return OperationEventPage{}, ErrOperationQueryForbidden
	}
	if operationID == "" || afterSequence < 0 || limit < 1 || limit > MaxOperationEventPageSize {
		return OperationEventPage{}, ErrOperationQueryInvalid
	}
	var page OperationEventPage
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		op, found, err := reader.GetOperation(auth.Namespace, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrOperationQueryNotFound
		}
		watermark, err := reader.GetOperationEventWatermark(auth.Namespace, operationID)
		if err != nil {
			return err
		}
		if afterSequence > watermark {
			return ErrOperationQueryInvalid
		}
		records, err := reader.ListOperationEvents(auth.Namespace, operationID, afterSequence, limit)
		if err != nil {
			return err
		}
		page = OperationEventPage{Events: make([]domain.OperationEvent, 0, len(records)), LastSequence: watermark, State: op.State}
		expected := afterSequence + 1
		for _, record := range records {
			if record.Sequence != expected || record.Sequence > watermark || record.CreatedAt < 0 || len(record.PayloadJSON) == 0 || len(record.PayloadJSON) > MaxOperationEventBytes || !json.Valid(record.PayloadJSON) {
				return ErrOperationQueryIntegrity
			}
			page.Events = append(page.Events, domain.OperationEvent{
				OperationID: operationID, Sequence: record.Sequence, Type: record.EventType,
				CreatedAt: record.CreatedAt, Payload: slices.Clone(record.PayloadJSON),
			})
			expected++
		}
		return nil
	})
	if err != nil {
		return OperationEventPage{}, err
	}
	return page, nil
}

// Decisions reads persisted selection history. It never replans against
// current configuration, so a row describes the choice made at execution time.
func (q *OperationQueries) Decisions(ctx context.Context, auth domain.AuthContext, operationID domain.ID) ([]domain.DecisionView, error) {
	if q == nil || authorizeOperationRead(auth) != nil {
		return nil, ErrOperationQueryForbidden
	}
	if operationID == "" {
		return nil, ErrOperationQueryInvalid
	}
	var decisions []domain.DecisionView
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		if _, found, err := reader.GetOperation(auth.Namespace, operationID); err != nil {
			return err
		} else if !found {
			return ErrOperationQueryNotFound
		}
		records, err := reader.ListOperationDecisions(auth.Namespace, operationID, MaxOperationDecisionRecords+1)
		if err != nil {
			return err
		}
		if len(records) > MaxOperationDecisionRecords {
			return ErrOperationQueryIntegrity
		}
		decisions = make([]domain.DecisionView, 0, len(records))
		for _, record := range records {
			if bytes.Equal(bytes.TrimSpace(record.TraceJSON), []byte("{}")) {
				continue
			}
			if record.DecisionID == "" || !slices.Contains([]string{"prepare", "rejected", "dispatch"}, record.Stage) || len(record.TraceJSON) == 0 || len(record.TraceJSON) > MaxOperationDecisionBytes || !json.Valid(record.TraceJSON) || record.CreatedAt < 0 {
				return ErrOperationQueryIntegrity
			}
			var trace struct {
				HookRegistryRevision      string                     `json:"hookRegistryRevision"`
				HookRegistryManifest      json.RawMessage            `json:"hookRegistryManifest"`
				PlannerCandidateOrder     []domain.ID                `json:"plannerCandidateOrder"`
				HookCandidateOrder        []domain.ID                `json:"hookCandidateOrder"`
				SelectedOfferID           domain.ID                  `json:"selectedOfferId"`
				PoolID                    domain.ID                  `json:"poolId"`
				PoolVersion               int64                      `json:"poolVersion"`
				MemberPriority            int64                      `json:"memberPriority"`
				ConfigRevision            domain.ID                  `json:"configRevision"`
				Candidates                []domain.CandidateDecision `json:"candidates"`
				PlannerCandidateTruncated bool                       `json:"plannerCandidateTruncated"`
				HookCandidateTruncated    bool                       `json:"hookCandidateTruncated"`
			}
			if err := json.Unmarshal(record.TraceJSON, &trace); err != nil {
				return ErrOperationQueryIntegrity
			}
			if len(trace.HookRegistryManifest) > 0 && !json.Valid(trace.HookRegistryManifest) {
				return ErrOperationQueryIntegrity
			}
			view := domain.DecisionView{
				DecisionID: record.DecisionID, AttemptID: record.AttemptID, PoolID: trace.PoolID,
				PoolVersion: trace.PoolVersion, OfferID: trace.SelectedOfferID, MemberPriority: trace.MemberPriority,
				ConfigRevision: trace.ConfigRevision, Stage: record.Stage, Candidates: trace.Candidates,
				TraceTruncated:       record.TraceTruncated || trace.PlannerCandidateTruncated || trace.HookCandidateTruncated,
				HookRegistryRevision: trace.HookRegistryRevision, HookRegistryManifest: slices.Clone(trace.HookRegistryManifest),
				PlannerCandidateOrder: slices.Clone(trace.PlannerCandidateOrder), HookCandidateOrder: slices.Clone(trace.HookCandidateOrder),
			}
			if view.Candidates == nil {
				view.Candidates = []domain.CandidateDecision{}
			}
			decisions = append(decisions, view)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return decisions, nil
}

// Output reads only committed chunks for an operation in the caller's
// namespace. The operation row, page, and byte count share one SQLite read
// snapshot so pagination and availability cannot race an append.
func (q *OperationQueries) Output(ctx context.Context, auth domain.AuthContext, operationID domain.ID, afterSequence int64, limit int) (domain.OutputPage, error) {
	if q == nil || authorizeOperationRead(auth) != nil {
		return domain.OutputPage{}, ErrOperationQueryForbidden
	}
	if operationID == "" || afterSequence < 0 || limit < 1 || limit > MaxOperationPageSize {
		return domain.OutputPage{}, ErrOperationQueryInvalid
	}
	var page domain.OutputPage
	err := q.store.Read(ctx, func(rx store.ReaderTx) error {
		reader, ok := rx.(store.OperationQueryReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		key := domain.OperationKey{Namespace: auth.Namespace, OperationID: operationID}
		op, found, err := reader.GetOperation(auth.Namespace, operationID)
		if err != nil {
			return err
		}
		if !found {
			return ErrOperationQueryNotFound
		}
		output, ok := rx.(store.OutputReaderTx)
		if !ok {
			return ErrOperationQueryStore
		}
		records, err := output.ListOperationOutput(key, afterSequence, limit+1)
		if err != nil {
			return err
		}
		chunks, size, err := output.CountOperationOutput(key)
		if err != nil {
			return err
		}
		if afterSequence > chunks {
			return ErrOperationQueryInvalid
		}
		if chunks < 0 || size < 0 || size > domain.MaxOperationOutputBytes ||
			(chunks > 0 && (op.VisibleOutputAt == nil || *op.VisibleOutputAt < 0)) || (chunks == 0 && op.VisibleOutputAt != nil) {
			return ErrOperationQueryIntegrity
		}
		complete := op.State != domain.Running
		availability := "none"
		if chunks > 0 {
			availability = "available"
		}
		page = domain.OutputPage{Chunks: make([]domain.OutputChunk, 0, min(len(records), limit)), Complete: complete, Availability: availability}
		if len(records) > limit {
			records = records[:limit]
			last := records[len(records)-1].Sequence
			page.NextSequence = &last
		}
		expected := afterSequence + 1
		for _, rec := range records {
			digest := sha256.Sum256(rec.Data)
			if rec.Sequence != expected || rec.CreatedAt < 0 || len(rec.Data) == 0 || len(rec.Data) > domain.MaxOutputChunkBytes || hex.EncodeToString(digest[:]) != rec.SHA256 {
				return ErrOperationQueryIntegrity
			}
			page.Chunks = append(page.Chunks, domain.OutputChunk{Sequence: rec.Sequence, Data: slices.Clone(rec.Data)})
			expected++
		}
		return nil
	})
	if err != nil {
		return domain.OutputPage{}, err
	}
	return page, nil
}

type operationCursor struct {
	UpdatedAt   domain.Millis `json:"updatedAt"`
	OperationID domain.ID     `json:"operationId"`
	State       string        `json:"state,omitempty"`
}

func encodeOperationCursor(at domain.Millis, id domain.ID, state string) string {
	raw, _ := json.Marshal(operationCursor{UpdatedAt: at, OperationID: id, State: state})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeOperationCursor(raw string) (domain.Millis, domain.ID, string, error) {
	if raw == "" {
		return 0, "", "", nil
	}
	if len(raw) > 512 {
		return 0, "", "", ErrOperationQueryInvalid
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || base64.RawURLEncoding.EncodeToString(data) != raw {
		return 0, "", "", ErrOperationQueryInvalid
	}
	var cursor operationCursor
	if err := jsonx.DecodeStrict(data, &cursor); err != nil || cursor.UpdatedAt <= 0 || cursor.OperationID == "" {
		return 0, "", "", ErrOperationQueryInvalid
	}
	return cursor.UpdatedAt, cursor.OperationID, cursor.State, nil
}

func validOperationListState(state string) bool {
	return state == "" || slices.Contains(domain.OperationStateValues(), domain.OperationState(state))
}

func projectOperation(reader store.OperationQueryReaderTx, record store.ClaimOperationRecord) (domain.OperationView, error) {
	var strategy domain.ExecuteInput
	if err := jsonx.DecodeStrict(record.StrategyJSON, &strategy); err != nil || strategy.Request.Domain == "" || strategy.Request.CapabilityProfile == "" {
		return domain.OperationView{}, ErrOperationQueryIntegrity
	}
	view := domain.OperationView{
		OperationID: record.OperationID, PoolID: record.PoolID, RequestDomain: strategy.Request.Domain, State: record.State,
		CancelRequested: record.CancelRequestedAt != nil,
		Attempts:        []domain.AttemptView{}, Resources: []domain.ResourceOutcome{}, Reconciliations: []domain.ReconciliationView{},
		UpdatedAt: record.UpdatedAt,
	}
	if len(record.ResultJSON) > 0 {
		var result domain.OperationResult
		if err := json.Unmarshal(record.ResultJSON, &result); err != nil || result.OperationID != record.OperationID || result.State != record.State {
			return domain.OperationView{}, ErrOperationQueryIntegrity
		}
		view.Result = &result
		view.Resources = append(view.Resources, result.Resources...)
	} else if record.State != domain.Running {
		return domain.OperationView{}, ErrOperationQueryIntegrity
	}
	attempts, err := reader.ListOperationAttempts(record.Namespace, record.OperationID)
	if err != nil {
		return domain.OperationView{}, err
	}
	for _, attempt := range attempts {
		view.Attempts = append(view.Attempts, domain.AttemptView{
			AttemptID: attempt.AttemptID, OfferID: attempt.OfferID, BindingID: attempt.BindingID, AccountID: attempt.AccountID,
			Phase: attempt.Phase, Outcome: attempt.Outcome, ProviderRequestID: attempt.ProviderRequestID,
		})
	}
	reconciliations, err := reader.ListOperationReconciliations(record.Namespace, record.OperationID)
	if err != nil {
		return domain.OperationView{}, err
	}
	for _, reconciliation := range reconciliations {
		var report domain.ExecutionReport
		var corrections []domain.ResourceOutcome
		if json.Unmarshal(reconciliation.EvidenceJSON, &report) != nil || json.Unmarshal(reconciliation.CorrectionJSON, &corrections) != nil {
			return domain.OperationView{}, ErrOperationQueryIntegrity
		}
		view.Reconciliations = append(view.Reconciliations, domain.ReconciliationView{
			ReconciliationID: reconciliation.ReconciliationID, ConfirmedOutcome: report.Outcome,
			RetrySafety: report.RetrySafety, EvidenceRefs: append([]domain.ID(nil), report.EndEvidenceRefs...),
			Corrections: corrections, CreatedAt: reconciliation.CreatedAt,
		})
	}
	return view, nil
}
