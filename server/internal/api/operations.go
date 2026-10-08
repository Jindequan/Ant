package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type OperationQueryCommands interface {
	Get(context.Context, domain.AuthContext, domain.ID) (domain.OperationView, error)
	List(context.Context, domain.AuthContext, string, int, string) (application.OperationPage, error)
	Output(context.Context, domain.AuthContext, domain.ID, int64, int) (domain.OutputPage, error)
}

type OperationDecisionCommands interface {
	Decisions(context.Context, domain.AuthContext, domain.ID) ([]domain.DecisionView, error)
}

type OperationCancelCommands interface {
	RequestCancel(context.Context, domain.AuthContext, domain.OperationKey, domain.ID) (domain.CancellationAck, error)
}

type OperationReconcileCommands interface {
	Reconcile(context.Context, domain.AuthContext, domain.ReconciliationInput) (domain.ReconciliationView, error)
}

type OperationReconcileInput struct {
	CommandID        domain.ID                `json:"commandId"`
	ConfirmedOutcome string                   `json:"confirmedOutcome"`
	RetrySafety      domain.RetrySafety       `json:"retrySafety"`
	EvidenceRefs     []domain.ID              `json:"evidenceRefs"`
	Corrections      []domain.ResourceOutcome `json:"corrections"`
}

type OperationSubmitCommands interface {
	Submit(context.Context, domain.AuthContext, domain.ExecuteInput) (domain.SubmitReply, error)
}

func OperationSubmitHandler(commands OperationSubmitCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/pools/{poolId}/operations", func(w http.ResponseWriter, r *http.Request) {
		if commands == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		_, auth, ok := configPrincipalFor(w, r, domain.PermExecute)
		if !ok {
			return
		}
		var input domain.ExecuteInput
		if !decodeConfigBody(w, r, &input) {
			return
		}
		poolID := domain.ID(r.PathValue("poolId"))
		if poolID == "" || input.PoolID != poolID {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		reply, err := commands.Submit(r.Context(), auth, input)
		if err != nil {
			writeOperationSubmitError(w, err)
			return
		}
		if err := ValidateSubmitReply(&reply); err != nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		status := http.StatusOK
		if reply.Kind == "accepted" {
			status = http.StatusAccepted
		} else if reply.Kind == "rejected" {
			status = http.StatusConflict
		}
		writeConfigJSON(w, status, reply)
	})
	return mux
}

func writeOperationSubmitError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrSubmitInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrSubmitForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrSubmitBusy):
		writeConfigError(w, http.StatusTooManyRequests, "Busy", "ant.error.busy", true, "retry_later")
	case errors.Is(err, application.ErrSubmitNotReady):
		writeConfigError(w, http.StatusServiceUnavailable, "NotReady", "ant.error.notReady", false, "check_configuration")
	case errors.Is(err, application.ErrSubmitConflict):
		writeConfigError(w, http.StatusConflict, "OperationIdConflict", "ant.error.operationIdConflict", false, "use_new_id")
	case errors.Is(err, application.ErrSubmitHostTakeover):
		writeConfigError(w, http.StatusServiceUnavailable, "HostUnavailable", "ant.error.hostUnavailable", true, "retry_or_query_by_id")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}

// OperationQueryHandler exposes namespace-scoped operation projections and,
// when a cancellation command is provided, the durable cancel-intent route.
// The acknowledgement records intent only; it never invents terminal state.
func OperationQueryHandler(queries OperationQueryCommands, cancel ...OperationCancelCommands) http.Handler {
	mux := http.NewServeMux()
	if decisionQueries, ok := queries.(OperationDecisionCommands); ok {
		mux.HandleFunc("GET /v1/operations/{operationId}/decisions", func(w http.ResponseWriter, r *http.Request) {
			_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
			if !ok {
				return
			}
			decisions, err := decisionQueries.Decisions(r.Context(), auth, domain.ID(r.PathValue("operationId")))
			if err != nil {
				writeOperationQueryError(w, err)
				return
			}
			writeConfigJSON(w, http.StatusOK, decisions)
		})
	}
	mux.HandleFunc("GET /v1/operations/{operationId}/output", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		after, limit := int64(0), application.DefaultOperationPageSize
		if values, exists := r.URL.Query()["afterSequence"]; exists {
			if len(values) != 1 {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			parsed, err := strconv.ParseInt(values[0], 10, 64)
			if err != nil || parsed < 0 {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			after = parsed
		}
		if values, exists := r.URL.Query()["limit"]; exists {
			if len(values) != 1 {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			parsed, err := strconv.Atoi(values[0])
			if err != nil || parsed < 1 || parsed > application.MaxOperationPageSize {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			limit = parsed
		}
		page, err := queries.Output(r.Context(), auth, domain.ID(r.PathValue("operationId")), after, limit)
		if err != nil {
			writeOperationQueryError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /v1/operations", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		limit := application.DefaultOperationPageSize
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > application.MaxOperationPageSize {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			limit = parsed
		}
		state := ""
		if values, exists := r.URL.Query()["state"]; exists {
			if len(values) != 1 || !validOperationListState(values[0]) || values[0] == "" {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			state = values[0]
		}
		page, err := queries.List(r.Context(), auth, r.URL.Query().Get("cursor"), limit, state)
		if err != nil {
			writeOperationQueryError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, page)
	})
	mux.HandleFunc("GET /v1/operations/{operationId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		view, err := queries.Get(r.Context(), auth, domain.ID(r.PathValue("operationId")))
		if err != nil {
			writeOperationQueryError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, view)
	})
	if len(cancel) > 0 && cancel[0] != nil {
		mux.HandleFunc("POST /v1/operations/{operationId}/cancel", func(w http.ResponseWriter, r *http.Request) {
			_, auth, ok := configPrincipalFor(w, r, domain.PermCancel)
			if !ok {
				return
			}
			var in OperationCancelInput
			if !decodeConfigBody(w, r, &in) {
				return
			}
			operationID := domain.ID(r.PathValue("operationId"))
			if operationID == "" || in.CommandID == "" {
				writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
				return
			}
			ack, err := cancel[0].RequestCancel(r.Context(), auth, domain.OperationKey{Namespace: auth.Namespace, OperationID: operationID}, in.CommandID)
			if err != nil {
				writeOperationCancelError(w, err)
				return
			}
			writeConfigJSON(w, http.StatusOK, ack)
		})
	}
	return mux
}

// OperationReconciliationHandler provides a separately composable management
// route so deployments can add the recovery command without changing the
// existing query/cancel handler wiring.
func OperationReconciliationHandler(reconciler OperationReconcileCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/attempts/{attemptId}/reconciliations", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		if reconciler == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
			return
		}
		var in OperationReconcileInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		attemptID := domain.ID(r.PathValue("attemptId"))
		if attemptID == "" || in.CommandID == "" || len(in.EvidenceRefs) == 0 || in.Corrections == nil {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		view, err := reconciler.Reconcile(r.Context(), auth, domain.ReconciliationInput{
			CommandID: in.CommandID, AttemptID: attemptID, ConfirmedOutcome: in.ConfirmedOutcome,
			RetrySafety: in.RetrySafety, EvidenceRefs: in.EvidenceRefs, Corrections: in.Corrections,
		})
		if err != nil {
			writeOperationReconcileError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusCreated, view)
	})
	return mux
}

func writeOperationReconcileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrOperationReconcileForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrOperationReconcileInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrOperationReconcileConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "reload_current")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}

func validOperationListState(state string) bool {
	return slices.Contains(domain.OperationStateValues(), domain.OperationState(state))
}

func writeOperationCancelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrOperationCancelForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrOperationCancelInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrOperationCancelConflict):
		writeConfigError(w, http.StatusConflict, "CommandConflict", "ant.error.conflict", false, "reload_current")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}

func writeOperationQueryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrOperationQueryForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrOperationQueryNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	case errors.Is(err, application.ErrOperationQueryInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrOperationQueryStore):
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}
