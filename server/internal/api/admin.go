package api

import (
	"context"
	"errors"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type AdminCommands interface {
	RefreshObservation(context.Context, domain.AuthContext, domain.ID, domain.ObservationEvidence) (domain.RefreshResult, error)
	Adjust(context.Context, domain.AuthContext, domain.AdjustmentInput) (domain.CommandAck, error)
	ClearHold(context.Context, domain.AuthContext, domain.ClearHoldInput) (domain.CommandAck, error)
}

type observationRefreshRequest struct {
	CommandID domain.ID `json:"commandId"`
	Evidence  struct {
		SourceRef                domain.ID     `json:"sourceRef"`
		ObservedAtMillis         domain.Millis `json:"observedAtMillis"`
		ExpiresAtMillis          domain.Millis `json:"expiresAtMillis"`
		Remaining                domain.Units  `json:"remaining"`
		CommittedAtStart         domain.Units  `json:"committedAtStart"`
		AdjustmentVersionAtStart int64         `json:"adjustmentVersionAtStart"`
	} `json:"evidence"`
}

type resourceAdjustmentRequest struct {
	CommandID       domain.ID    `json:"commandId"`
	WindowKey       domain.ID    `json:"windowKey"`
	Delta           domain.Units `json:"delta"`
	OriginalEventID domain.ID    `json:"originalEventId,omitempty"`
	EvidenceRefs    []domain.ID  `json:"evidenceRefs"`
}

func AdminHandler(commands AdminCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/resources/{resourceId}/observations", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		if commands == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_later")
			return
		}
		var input observationRefreshRequest
		if !decodeConfigBody(w, r, &input) {
			return
		}
		resourceID := domain.ID(r.PathValue("resourceId"))
		if resourceID == "" || input.CommandID == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		result, err := commands.RefreshObservation(r.Context(), auth, input.CommandID, domain.ObservationEvidence{
			ResourceID: resourceID, SourceRef: input.Evidence.SourceRef, ObservedAt: input.Evidence.ObservedAtMillis,
			ExpiresAt: input.Evidence.ExpiresAtMillis, Remaining: input.Evidence.Remaining,
			CommittedAtStart: input.Evidence.CommittedAtStart, AdjustmentVersionAtStart: input.Evidence.AdjustmentVersionAtStart,
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /v1/resources/{resourceId}/adjustments", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		if commands == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_later")
			return
		}
		var input resourceAdjustmentRequest
		if !decodeConfigBody(w, r, &input) {
			return
		}
		resourceID := domain.ID(r.PathValue("resourceId"))
		if resourceID == "" || input.CommandID == "" || input.WindowKey == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		ack, err := commands.Adjust(r.Context(), auth, domain.AdjustmentInput{
			CommandID: input.CommandID, ResourceID: resourceID, WindowKey: input.WindowKey, Delta: input.Delta,
			OriginalEventID: input.OriginalEventID, EvidenceRefs: input.EvidenceRefs,
		})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, ack)
	})
	mux.HandleFunc("POST /v1/holds/{holdId}/clear", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		if commands == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_later")
			return
		}
		var input struct {
			CommandID   domain.ID   `json:"commandId"`
			EvidenceIDs []domain.ID `json:"evidenceIds"`
		}
		if !decodeConfigBody(w, r, &input) {
			return
		}
		holdID := domain.ID(r.PathValue("holdId"))
		if holdID == "" || input.CommandID == "" || len(input.EvidenceIDs) == 0 {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		ack, err := commands.ClearHold(r.Context(), auth, domain.ClearHoldInput{CommandID: input.CommandID, HoldID: holdID, EvidenceIDs: input.EvidenceIDs})
		if err != nil {
			writeAdminError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, ack)
	})
	return mux
}

func writeAdminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrAdminForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrAdminInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrAdminNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "refresh_and_retry")
	case errors.Is(err, application.ErrAdminConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "review_current_state")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_later")
	}
}
