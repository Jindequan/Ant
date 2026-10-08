package api

import (
	"context"
	"errors"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type DeepSeekBalanceObserver interface {
	Observe(context.Context, domain.AuthContext, domain.ID, domain.ID, domain.ID) (application.DeepSeekBalanceObservation, error)
	Get(context.Context, domain.AuthContext, domain.ID) (application.DeepSeekBalanceObservation, error)
}

func DeepSeekBalanceHandler(service DeepSeekBalanceObserver) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/providers/deepseek/balance-observations", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in DeepSeekBalanceObserveInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.CommandID == "" || in.CredentialRef == "" || in.CredentialRevision == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		observation, err := service.Observe(r.Context(), auth, in.CommandID, in.CredentialRef, in.CredentialRevision)
		if err != nil {
			writeDeepSeekBalanceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, observation)
	})
	mux.HandleFunc("GET /v1/providers/deepseek/balance-observations/{commandId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		observation, err := service.Get(r.Context(), auth, domain.ID(r.PathValue("commandId")))
		if err != nil {
			writeDeepSeekBalanceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, observation)
	})
	return mux
}

func writeDeepSeekBalanceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrDeepSeekBalanceInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrDeepSeekBalanceForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrSecretNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	case errors.Is(err, application.ErrSecretConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "reload_current")
	case errors.Is(err, application.ErrDeepSeekBalanceNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	case errors.Is(err, application.ErrDeepSeekBalanceResponse):
		writeConfigError(w, http.StatusBadGateway, "InvalidProviderResponse", "ant.error.providerUnavailable", false, "check_provider_status")
	case errors.Is(err, application.ErrDeepSeekBalanceBusy):
		writeConfigError(w, http.StatusTooManyRequests, "Busy", "ant.error.busy", true, "retry_later")
	case errors.Is(err, application.ErrDeepSeekBalanceUnavailable):
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}
