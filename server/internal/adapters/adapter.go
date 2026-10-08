// Package adapters defines the ProviderAdapter extension port and its wire
// request/response value types. Adapters only encode/decode; they hold no
// HTTP client, no SQL, no secret resolution. The controlled Transport is the
// only caller of the network.
package adapters

import (
	"context"
	"io"

	"ant/server/internal/domain"
)

type EncodedRequest struct {
	Endpoint         string
	Method           string
	Headers          map[string]string // sendable headers only; secrets injected by Transport
	Body             []byte
	CredentialRef    domain.ID
	CredentialHeader string // optional header name where Transport injects the resolved secret
	CredentialField  string // secret field name; defaults to "value"
	CredentialPrefix string // non-secret auth prefix, e.g. "Bearer "
	MaxResponseBytes int64
}

type RawResponse struct {
	Status  int
	Headers map[string]string
	Body    io.ReadCloser
}

// ProviderAdapter is registered explicitly in code at composition time; no
// dynamic loading of unknown providers (DESIGN §2.2).
type ProviderAdapter interface {
	Descriptor() domain.ProviderDescriptor
	Encode(domain.DomainRequest, domain.ExecutionPlan) (EncodedRequest, error)
	Decode(context.Context, RawResponse, domain.ExecutionPlan, domain.ID, func([]byte) error) (domain.ExecutionReport, error)
}

// CapacityProbeRequest / CapacityReader: optional read-only probe capability,
// registered separately from the public DomainRequest path (§5.4).
type CapacityProbeRequest struct {
	ResourceID domain.ID
	BindingID  domain.ID
}

type CapacityReader interface {
	EncodeProbe(CapacityProbeRequest, domain.ExecutionPlan) (EncodedRequest, error)
	DecodeObservation(context.Context, RawResponse, domain.ExecutionPlan) (domain.ObservationEvidence, error)
}
