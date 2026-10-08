package domain

import (
	"reflect"
	"testing"
)

func candidate(id ID, priority int64) Candidate {
	return Candidate{
		Member: PoolMember{OfferID: id, Priority: priority, Enabled: true, Version: 1},
		Offer:  ResourceOffer{OfferID: id, Domain: "ai/text", CapabilityProfile: "chat", Enabled: true},
	}
}

func offerIDs(cands []Candidate) []ID {
	ids := make([]ID, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.Offer.OfferID)
	}
	return ids
}

func TestRankCandidatesPriorityThenOfferID(t *testing.T) {
	in := []Candidate{candidate("c", 20), candidate("b", 10), candidate("a", 10), candidate("d", 30)}
	got := RankCandidates(in)
	want := []ID{"a", "b", "c", "d"}
	if ids := offerIDs(got); !reflect.DeepEqual(ids, want) {
		t.Fatalf("want %v, got %v", want, ids)
	}
}

func TestRankCandidatesStableForEqualKeys(t *testing.T) {
	// Two candidates with identical (priority, offerID) keys must keep their
	// input relative order — distinguished here by member version.
	x := candidate("dup", 10)
	x.Member.Version = 1
	y := candidate("dup", 10)
	y.Member.Version = 2
	in := []Candidate{x, y, candidate("z", 5)}
	got := RankCandidates(in)
	// z (priority 5) sorts first; the two equal-key dup candidates follow in
	// input order.
	if got[0].Offer.OfferID != "z" {
		t.Fatalf("lowest priority must come first, got %v", offerIDs(got))
	}
	if got[1].Member.Version != 1 || got[2].Member.Version != 2 {
		t.Fatalf("equal keys must preserve input order, got versions %d,%d",
			got[1].Member.Version, got[2].Member.Version)
	}
}

func TestRankCandidatesIgnoresCostAndPreferredDiagnostics(t *testing.T) {
	// ExecutionPlan carries no cost field; vary plan content (a diagnostic
	// proxy for cost) and Reasons and assert the order does not move.
	cheap := candidate("b", 20)
	cheap.Plan.Requirements = []ResourceRequirement{}
	cheap.Reasons = []Reason{{Group: "quote", Code: "cost_low"}}
	expensive := candidate("a", 20)
	expensive.Plan.Requirements = []ResourceRequirement{
		{MetricID: "tokens", Unit: "token", UpperBound: 100000, Mode: "all_constraints"},
	}
	expensive.Reasons = []Reason{{Group: "quote", Code: "cost_high"}}
	got := RankCandidates([]Candidate{cheap, expensive})
	if ids := offerIDs(got); !reflect.DeepEqual(ids, []ID{"a", "b"}) {
		t.Fatalf("plan/cost diagnostics must not reorder, got %v", ids)
	}
}

func TestRankCandidatesDoesNotMutateInput(t *testing.T) {
	in := []Candidate{candidate("b", 20), candidate("a", 10)}
	keep := make([]Candidate, len(in))
	copy(keep, in)
	_ = RankCandidates(in)
	if !reflect.DeepEqual(in, keep) {
		t.Fatalf("input slice must not be modified, got %v", in)
	}
}

func TestRankCandidatesEmptyAndNil(t *testing.T) {
	if got := RankCandidates(nil); len(got) != 0 {
		t.Fatalf("nil input must yield empty output, got %v", got)
	}
	if got := RankCandidates([]Candidate{}); len(got) != 0 {
		t.Fatalf("empty input must yield empty output, got %v", got)
	}
}
