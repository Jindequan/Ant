package api

import (
	"context"
	"errors"
	"net/http"

	"ant/server/internal/application"
	"ant/server/internal/domain"
)

type PoolQueryCommands interface {
	Get(context.Context, domain.AuthContext, domain.ID) (application.PoolProjection, error)
	List(context.Context, domain.AuthContext) ([]application.PoolProjection, error)
}

type PoolOrderCommands interface {
	ReorderPool(context.Context, domain.AuthContext, domain.ID, domain.ReorderPoolInput) (domain.ResourcePool, error)
}

// PoolsHandler serves the authorized pool projections and the one authoritative
// full-group priority write. Mount behind AuthMiddleware.
func PoolsHandler(queries PoolQueryCommands, orders PoolOrderCommands, submit ...OperationSubmitCommands) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/pools", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		if queries == nil {
			writePoolUnavailable(w)
			return
		}
		values, err := queries.List(r.Context(), auth)
		if err != nil {
			writePoolQueryError(w, err)
			return
		}
		views := make([]PoolView, 0, len(values))
		for _, value := range values {
			views = append(views, toPoolView(value))
		}
		writeConfigJSON(w, http.StatusOK, views)
	})
	mux.HandleFunc("GET /v1/pools/{poolId}", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipalFor(w, r, domain.PermRead)
		if !ok {
			return
		}
		if queries == nil {
			writePoolUnavailable(w)
			return
		}
		value, err := queries.Get(r.Context(), auth, domain.ID(r.PathValue("poolId")))
		if err != nil {
			writePoolQueryError(w, err)
			return
		}
		writeConfigJSON(w, http.StatusOK, toPoolView(value))
	})
	mux.HandleFunc("PUT /v1/pools/{poolId}/order", func(w http.ResponseWriter, r *http.Request) {
		_, auth, ok := configPrincipal(w, r)
		if !ok {
			return
		}
		if orders == nil || queries == nil {
			writePoolUnavailable(w)
			return
		}
		var input domain.ReorderPoolInput
		if !decodeConfigBody(w, r, &input) {
			return
		}
		poolID := domain.ID(r.PathValue("poolId"))
		if poolID == "" || input.CommandID == "" || input.ExpectedPoolVersion < 1 || input.ExpectedConfigRevision == "" || len(input.Entries) == 0 {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		if err := ValidateIfMatch(r.Header.Get("If-Match"), input.ExpectedPoolVersion); err != nil {
			writeConfigError(w, http.StatusPreconditionFailed, "VersionMismatch", "ant.error.versionHeaderMismatch", false, "reload_current")
			return
		}
		if _, err := orders.ReorderPool(r.Context(), auth, poolID, input); err != nil {
			writePoolOrderError(w, err)
			return
		}
		view, err := queries.Get(r.Context(), auth, poolID)
		if err != nil {
			// The write may have committed before a read-side failure. Preserve the
			// command identity so clients can query the pool and safely replay.
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "query_current_state")
			return
		}
		writeConfigJSON(w, http.StatusOK, toPoolView(view))
	})
	if len(submit) > 0 && submit[0] != nil {
		mux.Handle("POST /v1/pools/{poolId}/operations", OperationSubmitHandler(submit[0]))
	}
	return mux
}

func toPoolView(value application.PoolProjection) PoolView {
	view := PoolView{Pool: value.Pool, Groups: make([]PoolGroup, 0, len(value.Groups))}
	for _, group := range value.Groups {
		out := PoolGroup{Domain: group.Domain, CapabilityProfile: group.CapabilityProfile, Members: make([]PoolMemberView, 0, len(group.Members))}
		for _, member := range group.Members {
			resources := make([]ResourceView, 0, len(member.Resources))
			for _, resource := range member.Resources {
				resources = append(resources, ResourceView{
					ResourceID: resource.ResourceID, WindowKey: resource.WindowKey, MetricID: resource.MetricID, Unit: resource.Unit,
					Committed: resource.Committed, AdjustmentVersion: resource.AdjustmentVersion,
					Available: resource.Available, Reserved: resource.Reserved, Active: resource.Active, Uncertain: resource.Uncertain,
					EvidenceStatus: resource.EvidenceStatus, HoldIDs: resource.HoldIDs, EncumbranceIDs: resource.EncumbranceIDs,
					ObservationRefreshNeeded: resource.ObservationRefreshNeeded,
				})
			}
			out.Members = append(out.Members, PoolMemberView{Member: member.Member, Offer: member.Offer, Status: member.Status, Reasons: member.Reasons, Resources: resources})
		}
		view.Groups = append(view.Groups, out)
	}
	return view
}

func writePoolQueryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, application.ErrPoolQueryForbidden):
		writeConfigError(w, http.StatusForbidden, "Forbidden", "ant.error.forbidden", false, "check_permissions")
	case errors.Is(err, application.ErrPoolQueryNotFound):
		writeConfigError(w, http.StatusNotFound, "NotFound", "ant.error.notFound", false, "check_id")
	default:
		writePoolUnavailable(w)
	}
}

func writePoolOrderError(w http.ResponseWriter, err error) {
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
		writeConfigError(w, http.StatusConflict, "ConfigConflict", "ant.error.configNotExecutable", false, "repair_configuration")
	default:
		writePoolUnavailable(w)
	}
}

func writePoolUnavailable(w http.ResponseWriter) {
	writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_or_query_by_id")
}
