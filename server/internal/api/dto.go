// Package api holds wire-only projections: HTTP envelopes, session/secret
// views, read projections for pools and resources. Internal port and permit
// types are never exposed here (K6: internal types must not become wire
// output). Strict JSON decoding delegates to internal/jsonx.
package api

import (
	"encoding/json"

	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
)

// ErrorEnvelope is the uniform error wire shape (08-API §1): inner ErrorDTO
// plus the server-assigned requestId.
type ErrorEnvelope struct {
	Error     domain.ErrorDTO `json:"error"`
	RequestID string          `json:"requestId"`
}

type LoginInput struct {
	Token string `json:"token"` // high-entropy bootstrap/session token; never logged.
}

type OperationCancelInput struct {
	CommandID domain.ID `json:"commandId"`
}

type SessionView struct {
	PrincipalID domain.ID           `json:"principalId"`
	Namespace   domain.ID           `json:"namespace"`
	Permissions []domain.Permission `json:"permissions"`
	CSRFToken   string              `json:"csrfToken"`
	ExpiresAt   domain.Millis       `json:"expiresAt"`
}

type SecretWriteInput struct {
	CommandID domain.ID `json:"commandId"`
	Value     string    `json:"value"` // write-only; never returned.
}

type SecretRotateInput struct {
	CommandID        domain.ID `json:"commandId"`
	ExpectedRevision domain.ID `json:"expectedRevision"`
	Value            string    `json:"value"` // write-only; never returned.
}

type DeepSeekBalanceObserveInput struct {
	CommandID          domain.ID `json:"commandId"`
	CredentialRef      domain.ID `json:"credentialRef"`
	CredentialRevision domain.ID `json:"credentialRevision"`
}

type SecretMetadata struct {
	Ref      domain.ID `json:"ref"`
	Revision domain.ID `json:"revision"`
	Mask     string    `json:"mask"`
}

// ResourceView is the authorized read projection; it never exposes
// domain.ResourceSnapshot directly and quantities are decimal strings.
type ResourceView struct {
	ResourceID               domain.ID     `json:"resourceId"`
	WindowKey                domain.ID     `json:"windowKey"`
	MetricID                 domain.ID     `json:"metricId"`
	Unit                     string        `json:"unit"`
	Committed                domain.Units  `json:"committed"`
	AdjustmentVersion        int64         `json:"adjustmentVersion"`
	Available                *domain.Units `json:"available,omitempty"`
	Reserved                 domain.Units  `json:"reserved"`
	Active                   domain.Units  `json:"active"`
	Uncertain                domain.Units  `json:"uncertain"`
	EvidenceStatus           string        `json:"evidenceStatus"`
	HoldIDs                  []domain.ID   `json:"holdIds"`
	EncumbranceIDs           []domain.ID   `json:"encumbranceIds"`
	ObservationRefreshNeeded bool          `json:"observationRefreshNeeded"`
}

type DecisionView = domain.DecisionView
type CandidateDecision = domain.CandidateDecision

type PoolMemberView struct {
	Member    domain.PoolMember    `json:"member"`
	Offer     domain.ResourceOffer `json:"offer"`
	Status    string               `json:"status"`
	Reasons   []domain.Reason      `json:"reasons"`
	Resources []ResourceView       `json:"resources"`
}

type PoolGroup struct {
	Domain            string           `json:"domain"`
	CapabilityProfile domain.ID        `json:"capabilityProfile"`
	Members           []PoolMemberView `json:"members"`
}

// PoolView groups members by domain/profile/unit; heterogeneous units are
// never summed into a single balance (K1/R22).
type PoolView struct {
	Pool   domain.ResourcePool `json:"pool"`
	Groups []PoolGroup         `json:"groups"`
}

// Draft create/update inputs (K1): create/update/get all go through the same
// draft mechanism; definition is a versioned ConfigDocument (see internal/config).
type CreateDraftInput struct {
	CommandID    domain.ID       `json:"commandId"`
	DraftID      domain.ID       `json:"draftId"`
	BaseRevision domain.ID       `json:"baseRevision,omitempty"`
	Definition   json.RawMessage `json:"definition"`
}

type UpdateDraftInput struct {
	CommandID            domain.ID       `json:"commandId"`
	ExpectedDraftVersion int64           `json:"expectedDraftVersion"`
	Definition           json.RawMessage `json:"definition,omitempty"`
}

// ConfigProjection is the active immutable revision returned by GET /v1/config.
type ConfigProjection struct {
	Revision domain.ID       `json:"revision"`
	Version  int64           `json:"version"`
	Document json.RawMessage `json:"document"`
}

// Strict decoding re-exports for handler use.
var (
	DecodeStrict    = jsonx.DecodeStrict
	EncodeCanonical = jsonx.EncodeCanonical
)
