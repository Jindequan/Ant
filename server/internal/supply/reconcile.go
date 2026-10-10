package supply

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/store"
)

// evidenceLimit caps a reading's evidence (design 11 §3.3, size caps).
const evidenceLimit = 16 << 10

// stableID is a short, stable id from its parts; the same parts always give
// the same id, so a re-read updates rows instead of creating them.
func stableID(prefix string, parts ...string) domain.ID {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return domain.ID(prefix + hex.EncodeToString(h[:12]))
}

// accountIdentity is the identity an inventory's account is stored under: the
// provider-reported one, or the credential itself when there is none, so
// credentials are merged only on what the provider said (§2.6).
func accountIdentity(cred Credential, inv observed.Inventory) string {
	if inv.Identity != "" {
		return inv.Identity
	}
	return "credential:" + string(cred.ID)
}

// reconcile records one read's inventory (§3.3): the account under its
// identity, its packages and resources under stable ids, and a reading for
// every observed resource whose value changed. A reading not newer than the
// stored one is skipped, not an error (§3.3, reading order). It returns the
// account id and how many readings were skipped.
func (e *Engine) reconcile(tx store.SupplyWriterTx, cred Credential, inv observed.Inventory, runID domain.ID, at domain.Millis) (domain.ID, int, error) {
	identity := accountIdentity(cred, inv)
	accountID, err := tx.UpsertSupplyAccount(store.SupplyAccountRecord{Namespace: e.ns, AccountID: stableID("acct-", string(cred.ProviderID), identity),
		ProviderID: cred.ProviderID, ProviderIdentity: identity, DisplayName: inv.DisplayName, UpdatedAt: at})
	if err != nil {
		return "", 0, err
	}
	skipped := 0
	for _, p := range inv.Packages {
		packageID := stableID("pkg-", string(accountID), p.TemplateRef)
		err := tx.UpsertSupplyPackage(store.SupplyPackageRecord{Namespace: e.ns, PackageID: packageID, AccountID: accountID, TemplateRef: p.TemplateRef,
			Kind: p.Kind, Basis: p.Basis, Plan: p.Plan, StartsAt: p.StartsAt, RenewsAt: p.RenewsAt, EndsAt: p.EndsAt, AutoRenew: p.AutoRenew,
			Live: p.Basis == "observed", DefinitionJSON: packageDefinition(p), UpdatedAt: at})
		if err != nil {
			return "", 0, err
		}
		for _, r := range p.Resources {
			resourceID := stableID("res-", string(packageID), r.TemplateRef, string(r.ScopeJSON))
			err := tx.UpsertSupplyResource(store.SupplyResourceRecord{Namespace: e.ns, ResourceID: resourceID,
				// the physical key names the capacity itself: no namespace, no Ant-side ids
				PhysicalKey: stableID("pk-", string(cred.ProviderID), identity, p.TemplateRef, r.TemplateRef, string(r.ScopeJSON)),
				PackageID:   packageID, TemplateRef: r.TemplateRef, MetricID: r.MetricID, Unit: r.Unit, QuantumExp: r.QuantumExp,
				Currency: r.Currency, SemanticsRef: r.SemanticsRef, SemanticsRevision: r.SemanticsRevision,
				WindowJSON: r.WindowJSON, ScopeJSON: r.ScopeJSON, UpdatedAt: at})
			if err != nil {
				return "", 0, err
			}
			if p.Basis != "observed" || (r.Limit == nil && r.Used == nil && r.Remaining == nil) {
				continue // declared values are never readings (§3.6)
			}
			evidence := r.Evidence
			if len(evidence) == 0 {
				evidence = []byte(`{}`)
			} else if len(evidence) > evidenceLimit {
				evidence = []byte(`{"truncated":true}`)
			}
			_, err = tx.AppendSupplyReadingIfChanged(store.SupplyReadingRecord{Namespace: e.ns, ReadingID: newID("rd-"), ResourceID: resourceID,
				ObservedAt: at, WindowStart: r.WindowStart, WindowEnd: r.WindowEnd, ResetBasis: r.ResetBasis,
				Limit: r.Limit, Used: r.Used, Remaining: r.Remaining, RunID: runID, EvidenceJSON: evidence})
			if errors.Is(err, store.ErrSupplyReadingOutOfOrder) {
				skipped++
				continue
			}
			if err != nil {
				return "", 0, err
			}
		}
	}
	return accountID, skipped, nil
}

func (e *Engine) offerRecords(cred domain.ID, offers []observed.Offer, at domain.Millis) []store.SupplyOfferRecord {
	out := make([]store.SupplyOfferRecord, 0, len(offers))
	seen := map[domain.ID]bool{}
	for _, o := range offers {
		id := stableID("offer-", string(cred), o.Endpoint, o.UpstreamModel)
		if seen[id] {
			continue
		}
		seen[id] = true
		draws, _ := json.Marshal(o.Draws)
		out = append(out, store.SupplyOfferRecord{Namespace: e.ns, OfferID: id, CredentialID: cred, Endpoint: o.Endpoint, Protocol: o.Protocol,
			UpstreamModel: o.UpstreamModel, ModelID: o.ModelID, DrawsJSON: draws, VerifiedAt: at})
	}
	return out
}

// packageDefinition keeps provider facts beyond the package columns:
// whether it accepts calls now and which resources are hard route guards.
func packageDefinition(p observed.PackageReading) []byte {
	definition := struct {
		Usable           *bool    `json:"usable,omitempty"`
		RouteConstraints []string `json:"routeConstraints,omitempty"`
	}{Usable: p.Usable, RouteConstraints: p.RouteConstraints}
	b, _ := json.Marshal(definition)
	return b
}
