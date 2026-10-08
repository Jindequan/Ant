package api

import (
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/domain/supply"
	"net/http"
)

// InventoryHandler configures non-executable resource supply. It shares the
// cookie/CSRF middleware and namespace grant boundary with executable config.
func InventoryHandler(service *application.InventoryService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/inventory", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		view, err := service.Read(r.Context(), auth)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, view)
	})
	mux.HandleFunc("PUT /v1/inventory", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		var body struct {
			CommandID       domain.ID       `json:"commandId"`
			ExpectedVersion *int64          `json:"expectedVersion"`
			Document        supply.Document `json:"document"`
		}
		if !decodeConfigBody(w, r, &body) {
			return
		}
		if body.ExpectedVersion == nil {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		in := application.InventoryInput{CommandID: body.CommandID, ExpectedVersion: *body.ExpectedVersion, Document: body.Document}
		if ValidateIfMatch(r.Header.Get("If-Match"), in.ExpectedVersion) != nil {
			writeConfigError(w, http.StatusPreconditionFailed, "VersionMismatch", "ant.error.versionHeaderMismatch", false, "reload_current")
			return
		}
		view, err := service.Save(r.Context(), auth, in)
		if err != nil {
			writeConfigServiceError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, view)
	})
	return mux
}
