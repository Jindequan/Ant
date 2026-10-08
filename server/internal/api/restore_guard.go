package api

import (
	"context"
	"errors"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type RestoreGuardCommands interface {
	Get(context.Context, domain.AuthContext) (application.RestoreGuardState, error)
	Set(context.Context, domain.AuthContext, application.RestoreGuardInput) (application.RestoreGuardState, error)
}

// RestoreGuardHandler exposes the audited global external-dispatch guard to
// operators. Mount behind AuthMiddleware; the application service repeats the
// manage-permission check inside the use case.
func RestoreGuardHandler(commands RestoreGuardCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/runtime/restore-guard", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		state, err := commands.Get(r.Context(), auth)
		if err != nil {
			writeRestoreGuardError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, state)
	})
	mux.HandleFunc("PUT /v1/runtime/restore-guard", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermManage)
		if !ok {
			return
		}
		var input application.RestoreGuardInput
		if !decodeConfigBody(w, r, &input) {
			return
		}
		state, err := commands.Set(r.Context(), auth, input)
		if err != nil {
			writeRestoreGuardError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, state)
	})
	return mux
}

func writeRestoreGuardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrRestoreGuardInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrRestoreGuardForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrRestoreGuardConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "reload_current")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}
