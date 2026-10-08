package api

import (
	"context"
	"errors"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/security"
)

type SecretCommands interface {
	Create(context.Context, domain.AuthContext, domain.ID, string) (security.SecretView, error)
	Rotate(context.Context, domain.AuthContext, domain.ID, domain.ID, domain.ID, string) (security.SecretView, error)
	Get(context.Context, domain.AuthContext, domain.ID) (security.SecretView, error)
}

// SecretHandler exposes write-only create/rotate and mask-only metadata read.
func SecretHandler(commands SecretCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/secrets", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in SecretWriteInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.CommandID == "" || in.Value == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		value, err := commands.Create(r.Context(), auth, in.CommandID, in.Value)
		if err != nil {
			writeSecretServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusCreated, SecretMetadata{Ref: value.Ref, Revision: value.Revision, Mask: value.Mask})
	})
	mux.HandleFunc("POST /v1/secrets/{secretId}/rotate", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in SecretRotateInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.CommandID == "" || in.ExpectedRevision == "" || in.Value == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		value, err := commands.Rotate(r.Context(), auth, domain.ID(r.PathValue("secretId")), in.CommandID, in.ExpectedRevision, in.Value)
		if err != nil {
			writeSecretServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, SecretMetadata{Ref: value.Ref, Revision: value.Revision, Mask: value.Mask})
	})
	mux.HandleFunc("GET /v1/secrets/{secretId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		value, err := commands.Get(r.Context(), auth, domain.ID(r.PathValue("secretId")))
		if err != nil {
			writeSecretServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, SecretMetadata{Ref: value.Ref, Revision: value.Revision, Mask: value.Mask})
	})
	return mux
}

func writeSecretServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrSecretInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrSecretForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrSecretNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	case errors.Is(err, application.ErrSecretConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "reload_current")
	case errors.Is(err, application.ErrSecretStore):
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}
