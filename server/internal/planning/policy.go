// Package planning defines the provider-independent request-policy port.
// Integrations depend on these contracts, not on the application runtime.
// Policies receive validated projections and can only propose constraints;
// the planner and ledger validate the proposal before reserving or sending.
package planning

import (
	"context"

	"ant/server/internal/config"
	"ant/server/internal/domain"
)

type Constraints struct {
	TargetModelID         domain.ID
	ResponseModelIDs      []domain.ID
	Requirements          []domain.ResourceRequirement
	Bounds                []domain.BoundEvidence
	DomainRevision        domain.ID
	ReportMappingRevision domain.ID
}

type Policy interface {
	Domain() string
	CapabilityProfile() domain.ID
	Build(context.Context, domain.ExecuteInput, config.OfferDef, config.BindingDef, config.AccountDef, []config.CompiledResource, []config.EvidenceDef) (Constraints, []domain.Reason, error)
}

// ProviderPolicy binds a policy to one provider while allowing several
// providers to serve the same domain/profile. Exact policies take precedence
// over a profile-wide Policy registered directly with the planner.
type ProviderPolicy interface {
	Policy
	ProviderTypeID() domain.ID
}
