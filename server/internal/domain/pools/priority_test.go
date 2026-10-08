package pools

import (
	"errors"
	"reflect"
	"testing"

	"ant/server/internal/domain"
)

func member(id domain.ID, priority int64) domain.PoolMember {
	return domain.PoolMember{OfferID: id, Priority: priority, Enabled: true, Version: 42}
}

func entriesFor(ms []domain.PoolMember, priority func(domain.ID) int64) []domain.ReorderPoolEntry {
	out := make([]domain.ReorderPoolEntry, 0, len(ms))
	for _, m := range ms {
		out = append(out, domain.ReorderPoolEntry{OfferID: m.OfferID, Priority: priority(m.OfferID)})
	}
	return out
}

func TestValidateReorderUpdatesPriorityKeepsOtherFields(t *testing.T) {
	current := []domain.PoolMember{member("a", 10), member("b", 20), member("c", 30)}
	entries := []domain.ReorderPoolEntry{
		{OfferID: "b", Priority: 5},
		{OfferID: "a", Priority: 7},
		{OfferID: "c", Priority: 7},
	}
	got, err := ValidateReorderEntries(current, entries)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []domain.PoolMember{
		{OfferID: "a", Priority: 7, Enabled: true, Version: 42},
		{OfferID: "b", Priority: 5, Enabled: true, Version: 42},
		{OfferID: "c", Priority: 7, Enabled: true, Version: 42},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("want %+v, got %+v", want, got)
	}
	// 7/7 stays 7/7: equal priorities are legal and never normalized.
	if got[0].Priority != 7 || got[2].Priority != 7 {
		t.Fatalf("equal priorities must be stored as submitted, got %+v", got)
	}
	// Input slices untouched.
	if current[0].Priority != 10 || len(entries) != 3 {
		t.Fatal("inputs must not be mutated")
	}
}

func TestValidateReorderExactlyOnceConstraint(t *testing.T) {
	current := []domain.PoolMember{member("a", 10), member("b", 20)}
	cases := []struct {
		name    string
		entries []domain.ReorderPoolEntry
	}{
		{"missing member", []domain.ReorderPoolEntry{{OfferID: "a", Priority: 1}}},
		{"duplicate member", []domain.ReorderPoolEntry{
			{OfferID: "a", Priority: 1}, {OfferID: "a", Priority: 2}, {OfferID: "b", Priority: 3},
		}},
		{"unknown member", []domain.ReorderPoolEntry{
			{OfferID: "a", Priority: 1}, {OfferID: "b", Priority: 2}, {OfferID: "zz", Priority: 3},
		}},
	}
	for _, tc := range cases {
		if _, err := ValidateReorderEntries(current, tc.entries); !errors.Is(err, ErrReorderCoverage) {
			t.Fatalf("%s: want ErrReorderCoverage, got %v", tc.name, err)
		}
	}
}

func TestValidateReorderEmptyRules(t *testing.T) {
	got, err := ValidateReorderEntries(nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("both empty must return empty slice, got %+v %v", got, err)
	}
	current := []domain.PoolMember{member("a", 10)}
	if _, err := ValidateReorderEntries(current, nil); !errors.Is(err, ErrEmptyEntries) {
		t.Fatalf("empty entries over non-empty pool must fail, got %v", err)
	}
	// Entries over an empty pool are all unknown offers.
	if _, err := ValidateReorderEntries(nil, []domain.ReorderPoolEntry{{OfferID: "a", Priority: 1}}); !errors.Is(err, ErrReorderCoverage) {
		t.Fatalf("entries over empty pool must fail coverage, got %v", err)
	}
}

func TestValidateReorderPriorityRange(t *testing.T) {
	current := []domain.PoolMember{member("a", 10), member("b", 20)}
	okLow := entriesFor(current, func(domain.ID) int64 { return 0 })
	if _, err := ValidateReorderEntries(current, okLow); err != nil {
		t.Fatalf("priority 0 must be legal: %v", err)
	}
	okHigh := entriesFor(current, func(domain.ID) int64 { return 2147483647 })
	if _, err := ValidateReorderEntries(current, okHigh); err != nil {
		t.Fatalf("priority 2147483647 must be legal: %v", err)
	}
	for _, p := range []int64{-1, 2147483648, 1 << 40} {
		bad := entriesFor(current, func(domain.ID) int64 { return p })
		if _, err := ValidateReorderEntries(current, bad); !errors.Is(err, ErrPriorityRange) {
			t.Fatalf("priority %d must be rejected, got %v", p, err)
		}
	}
}

func TestSortMembersPriorityThenOfferIDStable(t *testing.T) {
	members := []domain.PoolMember{
		{OfferID: "b", Priority: 20},
		{OfferID: "y", Priority: 10},
		{OfferID: "a", Priority: 10},
		{OfferID: "a0", Priority: 20},
	}
	SortMembers(members)
	want := []domain.PoolMember{
		{OfferID: "a", Priority: 10},
		{OfferID: "y", Priority: 10},
		{OfferID: "a0", Priority: 20},
		{OfferID: "b", Priority: 20},
	}
	if !reflect.DeepEqual(members, want) {
		t.Fatalf("want %+v, got %+v", want, members)
	}
}

func TestSortMembersFullyEqualKeysStable(t *testing.T) {
	members := []domain.PoolMember{
		{OfferID: "dup", Priority: 7, Version: 1},
		{OfferID: "dup", Priority: 7, Version: 2},
		{OfferID: "dup", Priority: 7, Version: 3},
	}
	SortMembers(members)
	for i, want := range []int64{1, 2, 3} {
		if members[i].Version != want {
			t.Fatalf("fully equal keys must keep input order, position %d has version %d", i, members[i].Version)
		}
	}
}
