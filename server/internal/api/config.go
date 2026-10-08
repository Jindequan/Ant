package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/security"
)

const maxConfigBodyBytes = 1 << 20

// ConfigCommands is the use-case surface required by the configuration HTTP
// routes. Keeping handlers on this interface makes HTTP validation testable
// without bypassing the application transaction boundary.
type ConfigCommands interface {
	Read(ctx context.Context, auth domain.AuthContext) (domain.ConfigDraft, error)
	SaveDraft(ctx context.Context, auth domain.AuthContext, in application.ConfigDraftInput) (domain.ConfigDraft, error)
	GetDraft(ctx context.Context, auth domain.AuthContext, draftID domain.ID) (domain.ConfigDraft, error)
	ValidateDraft(ctx context.Context, auth domain.AuthContext, draftID domain.ID, expectedVersion int64) (domain.ConfigDraft, error)
	ActivateDraft(ctx context.Context, auth domain.AuthContext, in domain.ActivationInput) (domain.ActivationResult, error)
}

// ConfigHandler exposes the persisted configuration workflow. Mount the
// returned handler behind AuthMiddleware; every operation still rechecks the
// principal and the required read/configure grant before invoking the service.
func ConfigHandler(commands ConfigCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/config", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		value, err := commands.Read(r.Context(), auth)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, ConfigProjection{Revision: value.BaseRevision, Version: value.Version, Document: value.Definition})
	})
	mux.HandleFunc("POST /v1/config/drafts", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in CreateDraftInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.CommandID == "" || in.DraftID == "" || len(in.Definition) == 0 {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		value, err := commands.SaveDraft(r.Context(), auth, application.ConfigDraftInput{
			CommandID: in.CommandID, DraftID: in.DraftID, BaseRevision: in.BaseRevision, Definition: in.Definition,
		})
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusCreated, value)
	})
	mux.HandleFunc("GET /v1/config/drafts/{draftId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		value, err := commands.GetDraft(r.Context(), auth, domain.ID(r.PathValue("draftId")))
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("PATCH /v1/config/drafts/{draftId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in UpdateDraftInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.CommandID == "" || in.ExpectedDraftVersion < 1 || len(in.Definition) == 0 {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		if err := ValidateIfMatch(r.Header.Get("If-Match"), in.ExpectedDraftVersion); err != nil {
			writeConfigError(w, http.StatusPreconditionFailed, "VersionMismatch", "ant.error.versionHeaderMismatch", false, "reload_current")
			return
		}
		value, err := commands.SaveDraft(r.Context(), auth, application.ConfigDraftInput{
			CommandID: in.CommandID, DraftID: domain.ID(r.PathValue("draftId")), ExpectedVersion: in.ExpectedDraftVersion, Definition: in.Definition,
		})
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("POST /v1/config/drafts/{draftId}/validate", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		// Read first to bind If-Match to the current draft version; ValidateDraft
		// repeats the version check in its writer transaction, closing the race.
		id := domain.ID(r.PathValue("draftId"))
		current, err := commands.GetDraft(r.Context(), auth, id)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		if err := ValidateIfMatch(r.Header.Get("If-Match"), current.Version); err != nil {
			writeConfigError(w, http.StatusPreconditionFailed, "VersionMismatch", "ant.error.versionHeaderMismatch", false, "reload_current")
			return
		}
		value, err := commands.ValidateDraft(r.Context(), auth, id, current.Version)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, value)
	})
	mux.HandleFunc("POST /v1/config/drafts/{draftId}/activate", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var in domain.ActivationInput
		if !decodeConfigBody(w, r, &in) {
			return
		}
		if in.DraftID != domain.ID(r.PathValue("draftId")) || in.CommandID == "" || in.ExpectedDraftVersion < 1 {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		value, err := commands.ActivateDraft(r.Context(), auth, in)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, value)
	})
	return mux
}

func configPrincipal(w http.ResponseWriter, r *http.Request) (security.Principal, domain.AuthContext, bool) {
	return configPrincipalFor(w, r, domain.PermConfigure)
}

func configPrincipalFor(w http.ResponseWriter, r *http.Request, required domain.Permission) (security.Principal, domain.AuthContext, bool) {
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeConfigError(w, http.StatusUnauthorized, "Unauthenticated", "ant.error.unauthenticated", false, "login")
		return security.Principal{}, domain.AuthContext{}, false
	}
	if !security.Can(p, required, p.Namespace) && !(required == domain.PermRead && security.Can(p, domain.PermConfigure, p.Namespace)) {
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
		return p, domain.AuthContext{}, false
	}
	return p, domain.AuthContext{PrincipalID: p.PrincipalID, Namespace: p.Namespace, Permissions: p.Permissions}, true
}

func decodeConfigBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxConfigBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
		return false
	}
	if err := DecodeStrict(body, out); err != nil {
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
		return false
	}
	return true
}

func writeConfigJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeConfigError(w http.ResponseWriter, status int, code, messageKey string, retryable bool, action string) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// Request IDs aid correlation; entropy failure must not make an already
		// rejected request turn into a 500 or expose internal details.
		copy(raw[:], "request-id-fallback")
	}
	writeConfigJSON(w, status, ErrorEnvelope{Error: domain.ErrorDTO{Code: code, MessageKey: messageKey, Retryable: retryable, Action: action}, RequestID: hex.EncodeToString(raw[:])})
}

func writeConfigServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrConfigInvalid):
		writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
	case errors.Is(err, application.ErrConfigForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrConfigNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	case errors.Is(err, application.ErrConfigConflict):
		writeConfigError(w, http.StatusConflict, "Conflict", "ant.error.conflict", false, "reload_current")
	case errors.Is(err, application.ErrConfigNotExecutable):
		writeConfigError(w, http.StatusUnprocessableEntity, "ConfigNotExecutable", "ant.error.configNotExecutable", false, "fix_fields")
	case errors.Is(err, application.ErrConfigStore):
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	default:
		writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
	}
}
