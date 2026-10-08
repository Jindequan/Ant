package domain

import "sort"

// rankCandidates is the internal pure ordering of DESIGN §6: member priority
// ascending, ties broken by offerID ascending. It only orders — it never
// changes admission facts. Cost is diagnostic and never participates in the
// key; preferred affinity never reorders (07 §3: required affinity and spend
// policy are hard gates applied before ranking, and priority is never relaxed
// by them or by anything else).
func RankCandidates(cands []Candidate) []Candidate {
	out := make([]Candidate, len(cands))
	copy(out, cands)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Member.Priority != out[j].Member.Priority {
			return out[i].Member.Priority < out[j].Member.Priority
		}
		return out[i].Offer.OfferID < out[j].Offer.OfferID
	})
	return out
}
