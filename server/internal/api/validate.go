package api

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ant/server/internal/domain"
)

// Wire-union validators (K6): illegal kind/state combinations and
// header/body version conflicts are rejected at the codec boundary, never
// silently coerced. The same rules are mirrored by web/src/api/validate.ts.

var (
	ErrIllegalSubmitVariant  = errors.New("submitReply: illegal kind/state combination")
	ErrIllegalResultState    = errors.New("operationResult: illegal state enum")
	ErrVersionHeaderMismatch = errors.New("ifMatch: header does not match body version")
)

// ValidateSubmitReply enforces the SubmitReply union exactly as documented in
// contracts/openapi.yaml (the single rule source, mirrored by
// web/src/api/validate.ts — both sides must agree on every fixture):
//
//	accepted   → state=="running", operationUrl/eventsUrl present,
//	             no operation/result/error
//	inProgress → operation present with state running (and top-level state,
//	             when present, is running), no result/error
//	replay     → result present with a terminal state (top-level state, when
//	             present, matches it), no error
//	rejected   → error present, no operation/result (operationId echo allowed)
func ValidateSubmitReply(r *domain.SubmitReply) error {
	if r == nil {
		return ErrIllegalSubmitVariant
	}
	switch r.Kind {
	case "accepted":
		if r.State != domain.Running || r.OperationURL == "" || r.EventsURL == "" ||
			r.Operation != nil || r.Result != nil || r.Error != nil {
			return fmt.Errorf("%w: accepted requires state=running and URLs only", ErrIllegalSubmitVariant)
		}
	case "inProgress":
		if r.Operation == nil || r.Operation.State != domain.Running || r.Result != nil || r.Error != nil {
			return fmt.Errorf("%w: inProgress requires a running operation projection", ErrIllegalSubmitVariant)
		}
		if r.State != "" && r.State != domain.Running {
			return fmt.Errorf("%w: inProgress top state must be running, got %q", ErrIllegalSubmitVariant, r.State)
		}
	case "replay":
		if r.Result == nil || r.Error != nil {
			return fmt.Errorf("%w: replay requires persisted result", ErrIllegalSubmitVariant)
		}
		if terminal := r.Result.State; terminal != domain.Succeeded && terminal != domain.Failed &&
			terminal != domain.Cancelled && terminal != domain.Unknown {
			return fmt.Errorf("%w: replay result must be terminal, got %q", ErrIllegalSubmitVariant, terminal)
		}
		if r.State != "" && r.State != r.Result.State {
			return fmt.Errorf("%w: replay top state %q must match result state %q", ErrIllegalSubmitVariant, r.State, r.Result.State)
		}
	case "rejected":
		if r.Error == nil || r.Operation != nil || r.Result != nil {
			return fmt.Errorf("%w: rejected requires error and no operation", ErrIllegalSubmitVariant)
		}
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrIllegalSubmitVariant, r.Kind)
	}
	return nil
}

// ValidateOperationResult checks the terminal-state enum on results.
func ValidateOperationResult(r *domain.OperationResult) error {
	switch r.State {
	case domain.Succeeded, domain.Failed, domain.Cancelled, domain.Unknown:
		return nil
	case domain.Running:
		return fmt.Errorf("%w: results are terminal, got running", ErrIllegalResultState)
	default:
		return fmt.Errorf("%w: %q", ErrIllegalResultState, r.State)
	}
}

// ValidateIfMatch enforces that the If-Match header equals the body-borne
// expected version; a conflict is an explicit rejection (K6), never a silent
// override by either value.
func ValidateIfMatch(headerValue string, expectedVersion int64) error {
	hv := strings.TrimSpace(headerValue)
	if hv == "" || expectedVersion < 0 || strings.HasPrefix(hv, "W/") || strings.Contains(hv, ",") || hv == "*" {
		return fmt.Errorf("%w: missing If-Match", ErrVersionHeaderMismatch)
	}
	// A weak entity tag never satisfies If-Match's strong comparison. Accept
	// the legacy bare integer and the documented quoted form, but reject
	// malformed or partially quoted values instead of normalizing them.
	if strings.HasPrefix(hv, `"`) || strings.HasSuffix(hv, `"`) {
		if len(hv) < 2 || hv[0] != '"' || hv[len(hv)-1] != '"' {
			return fmt.Errorf("%w: malformed If-Match %q", ErrVersionHeaderMismatch, headerValue)
		}
		hv = hv[1 : len(hv)-1]
	}
	got, err := strconv.ParseInt(hv, 10, 64)
	if err != nil || strconv.FormatInt(got, 10) != hv {
		return fmt.Errorf("%w: unparsable If-Match %q", ErrVersionHeaderMismatch, headerValue)
	}
	if got != expectedVersion {
		return fmt.Errorf("%w: header %d vs body %d", ErrVersionHeaderMismatch, got, expectedVersion)
	}
	return nil
}
