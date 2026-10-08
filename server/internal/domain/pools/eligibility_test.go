package pools

import (
	"reflect"
	"testing"

	"ant/server/internal/domain"
)

func eligCandidate(offer string, mutate func(*domain.Candidate)) domain.Candidate {
	c := domain.Candidate{
		Member: domain.PoolMember{OfferID: domain.ID(offer), Priority: 10, Enabled: true, Version: 1},
		Offer: domain.ResourceOffer{
			OfferID:           domain.ID(offer),
			BindingID:         domain.ID("bind-" + offer),
			Domain:            "ai/text",
			CapabilityProfile: "chat",
			Enabled:           true,
		},
	}
	if mutate != nil {
		mutate(&c)
	}
	return c
}

func hasReason(rs []domain.Reason, code string) bool {
	for _, r := range rs {
		if r.Code == code {
			return true
		}
	}
	return false
}

func TestFilterEligibleAcceptsCompatibleCandidate(t *testing.T) {
	in := EligibilityInput{Domain: "ai/text", CapabilityProfile: "chat", AllowMetered: true}
	eligible, rejected := FilterEligible([]domain.Candidate{eligCandidate("o1", nil)}, in)
	if len(eligible) != 1 || len(rejected) != 0 {
		t.Fatalf("compatible candidate must pass, got %+v / %+v", eligible, rejected)
	}
}

func TestFilterEligibleDomainProfileAndEnabledGates(t *testing.T) {
	in := EligibilityInput{Domain: "ai/text", CapabilityProfile: "chat", AllowMetered: true}
	cands := []domain.Candidate{
		eligCandidate("wrong-domain", func(c *domain.Candidate) { c.Offer.Domain = "email/send" }),
		eligCandidate("wrong-profile", func(c *domain.Candidate) { c.Offer.CapabilityProfile = "vision" }),
		eligCandidate("offer-off", func(c *domain.Candidate) { c.Offer.Enabled = false }),
		eligCandidate("member-off", func(c *domain.Candidate) { c.Member.Enabled = false }),
	}
	eligible, rejected := FilterEligible(cands, in)
	if len(eligible) != 0 {
		t.Fatalf("all candidates must be filtered, got %+v", eligible)
	}
	wantCodes := map[domain.ID]string{
		"wrong-domain":  "domain_mismatch",
		"wrong-profile": "capability_profile_mismatch",
		"offer-off":     "offer_disabled",
		"member-off":    "member_disabled",
	}
	if len(rejected) != len(wantCodes) {
		t.Fatalf("want %d rejections, got %+v", len(wantCodes), rejected)
	}
	for _, r := range rejected {
		want, ok := wantCodes[r.OfferID]
		if !ok || len(r.Reasons) != 1 || r.Reasons[0].Code != want {
			t.Fatalf("rejection for %s: want code %s, got %+v", r.OfferID, want, r.Reasons)
		}
	}
}

func TestFilterEligibleRequiredAffinityIsHardGate(t *testing.T) {
	in := EligibilityInput{
		Domain:            "ai/text",
		CapabilityProfile: "chat",
		AllowMetered:      true,
		RequiredAffinity:  &domain.Affinity{Mode: domain.AffinityModeRequired, BindingID: "bind-target", AccountID: "acc-1"},
		AccountByBinding:  map[domain.ID]domain.ID{"bind-target": "acc-1", "bind-other": "acc-2"},
	}
	cands := []domain.Candidate{
		eligCandidate("match", func(c *domain.Candidate) { c.Offer.BindingID = "bind-target" }),
		eligCandidate("binding-miss", func(c *domain.Candidate) { c.Offer.BindingID = "bind-other" }),
		eligCandidate("account-unverifiable", func(c *domain.Candidate) { c.Offer.BindingID = "bind-unknown" }),
	}

	eligible, rejected := FilterEligible(cands, in)
	if len(eligible) != 1 || eligible[0].Offer.OfferID != "match" {
		t.Fatalf("exact binding+account match must pass, got %+v / %+v", eligible, rejected)
	}
	if !hasReason(rejected[0].Reasons, "affinity_binding_mismatch") {
		t.Fatalf("binding mismatch expected, got %+v", rejected[0])
	}
	if !hasReason(rejected[1].Reasons, "affinity_account_unverifiable") {
		t.Fatalf("unverifiable account must fail closed, got %+v", rejected[1])
	}
}

func TestFilterEligibleRequiredAccountMismatch(t *testing.T) {
	in := EligibilityInput{
		Domain:            "ai/text",
		CapabilityProfile: "chat",
		AllowMetered:      true,
		RequiredAffinity:  &domain.Affinity{Mode: domain.AffinityModeRequired, AccountID: "acc-1"},
		AccountByBinding:  map[domain.ID]domain.ID{"bind-o1": "acc-2"},
	}
	_, rejected := FilterEligible([]domain.Candidate{eligCandidate("o1", nil)}, in)
	if len(rejected) != 1 || !hasReason(rejected[0].Reasons, "affinity_account_mismatch") {
		t.Fatalf("account mismatch must reject, got %+v", rejected)
	}
}

func TestFilterEligibleRequiredAffinityUnspecifiedFailsClosed(t *testing.T) {
	in := EligibilityInput{
		Domain:            "ai/text",
		CapabilityProfile: "chat",
		AllowMetered:      true,
		RequiredAffinity:  &domain.Affinity{Mode: domain.AffinityModeRequired},
	}
	_, rejected := FilterEligible([]domain.Candidate{eligCandidate("o1", nil)}, in)
	if len(rejected) != 1 || !hasReason(rejected[0].Reasons, "affinity_unspecified") {
		t.Fatalf("required affinity with no target must fail closed, got %+v", rejected)
	}
}

func TestFilterEligiblePreferredAffinityIsDiagnosticOnly(t *testing.T) {
	in := EligibilityInput{
		Domain:            "ai/text",
		CapabilityProfile: "chat",
		AllowMetered:      true,
		RequiredAffinity:  &domain.Affinity{Mode: "preferred", AccountID: "acc-somewhere-else"},
		AccountByBinding:  map[domain.ID]domain.ID{"bind-o1": "acc-1"},
	}
	eligible, rejected := FilterEligible([]domain.Candidate{eligCandidate("o1", nil)}, in)
	if len(eligible) != 1 || len(rejected) != 0 {
		t.Fatalf("preferred affinity must never filter, got %+v / %+v", eligible, rejected)
	}
}

func TestFilterEligibleMeteredGate(t *testing.T) {
	meteredIn := EligibilityInput{
		Domain:            "ai/text",
		CapabilityProfile: "chat",
		AllowMetered:      false,
		MeteredOfferIDs:   map[domain.ID]bool{"paid": true, "free": false},
	}
	cands := []domain.Candidate{eligCandidate("paid", nil), eligCandidate("free", nil)}
	eligible, rejected := FilterEligible(cands, meteredIn)
	if len(eligible) != 1 || eligible[0].Offer.OfferID != "free" {
		t.Fatalf("metered offer must be gated when allowMetered=false, got %+v / %+v", eligible, rejected)
	}
	if !hasReason(rejected[0].Reasons, "allowMetered") || rejected[0].Reasons[0].Group != "spend_policy" {
		t.Fatalf("want spend_policy/allowMetered, got %+v", rejected[0])
	}
	// The same pool with allowMetered=true passes.
	allowedIn := meteredIn
	allowedIn.AllowMetered = true
	eligible, _ = FilterEligible(cands, allowedIn)
	if len(eligible) != 2 {
		t.Fatalf("allowMetered=true must admit both, got %+v", eligible)
	}
}

func TestFilterEligibleAccumulatesAllReasons(t *testing.T) {
	in := EligibilityInput{
		Domain:            "email/send",
		CapabilityProfile: "vision",
		AllowMetered:      false,
		RequiredAffinity:  &domain.Affinity{Mode: domain.AffinityModeRequired, BindingID: "bind-target"},
		MeteredOfferIDs:   map[domain.ID]bool{"bad": true},
	}
	cands := []domain.Candidate{eligCandidate("bad", func(c *domain.Candidate) { c.Member.Enabled = false })}
	_, rejected := FilterEligible(cands, in)
	if len(rejected) != 1 {
		t.Fatalf("want one rejection, got %+v", rejected)
	}
	for _, want := range []string{"member_disabled", "domain_mismatch", "capability_profile_mismatch", "affinity_binding_mismatch", "allowMetered"} {
		if !hasReason(rejected[0].Reasons, want) {
			t.Fatalf("rejection must accumulate %s, got %+v", want, rejected[0].Reasons)
		}
	}
}

func TestFilterEligiblePreservesInputOrder(t *testing.T) {
	in := EligibilityInput{Domain: "ai/text", CapabilityProfile: "chat", AllowMetered: true}
	cands := []domain.Candidate{eligCandidate("c", nil), eligCandidate("a", nil), eligCandidate("b", nil)}
	eligible, _ := FilterEligible(cands, in)
	got := make([]domain.ID, 0, len(eligible))
	for _, c := range eligible {
		got = append(got, c.Offer.OfferID)
	}
	if !reflect.DeepEqual(got, []domain.ID{"c", "a", "b"}) {
		t.Fatalf("eligible order must follow input, got %v", got)
	}
}
