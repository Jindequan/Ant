package resources

import (
	"reflect"
	"testing"

	"ant/server/internal/domain"
)

func pool(id string, available domain.Units, metered, expired bool) EntitlementPool {
	return EntitlementPool{PoolID: domain.ID(id), Available: available, Metered: metered, Expired: expired}
}

func TestWaterfallInputOrderImmutable(t *testing.T) {
	pools := []EntitlementPool{
		pool("paid-1", 40, true, false),
		pool("free-1", 40, false, false),
		pool("paid-2", 40, true, false),
	}
	allocs, admitted, reasons := AllocateWaterfall(pools, 100, true)
	if !admitted || len(reasons) != 0 {
		t.Fatalf("want admitted, got %v %v", admitted, reasons)
	}
	want := []EntitlementAllocation{
		{PoolID: "paid-1", Units: 40},
		{PoolID: "free-1", Units: 40},
		{PoolID: "paid-2", Units: 20},
	}
	if !reflect.DeepEqual(allocs, want) {
		t.Fatalf("input order must be preserved exactly, got %+v", allocs)
	}
}

func TestWaterfallFreePoolsFirstExhaustedBeforeMetered(t *testing.T) {
	pools := []EntitlementPool{
		pool("free-a", 30, false, false),
		pool("free-b", 10, false, false),
		pool("paid-a", 50, true, false),
	}
	allocs, admitted, _ := AllocateWaterfall(pools, 35, true)
	if !admitted {
		t.Fatal("want admitted")
	}
	want := []EntitlementAllocation{
		{PoolID: "free-a", Units: 30},
		{PoolID: "free-b", Units: 5},
	}
	if !reflect.DeepEqual(allocs, want) {
		t.Fatalf("free pools must be consumed in order before paid, got %+v", allocs)
	}
}

func TestWaterfallMeteredHardGateRefusesWholeAllocation(t *testing.T) {
	// The metered pool stands first in the verified path; a later free pool
	// with enough balance must NOT be reachable by skipping it.
	pools := []EntitlementPool{
		pool("paid-a", 50, true, false),
		pool("free-b", 100, false, false),
	}
	allocs, admitted, reasons := AllocateWaterfall(pools, 40, false)
	if admitted || len(allocs) != 0 {
		t.Fatalf("metered gate must refuse the whole allocation, got admitted=%v allocs=%+v", admitted, allocs)
	}
	if len(reasons) != 1 || reasons[0].Group != "spend_policy" || reasons[0].Code != "allowMetered" {
		t.Fatalf("want spend_policy/allowMetered, got %v", reasons)
	}
	if reasons[0].ScopeRef != "paid-a" {
		t.Fatalf("gate must scope the blocking pool, got %v", reasons[0])
	}
}

func TestWaterfallInsufficientRefusesWhole(t *testing.T) {
	pools := []EntitlementPool{
		pool("free-a", 40, false, false),
		pool("paid-a", 40, true, false),
	}
	allocs, admitted, reasons := AllocateWaterfall(pools, 100, true)
	if admitted || len(allocs) != 0 {
		t.Fatalf("insufficient need must refuse whole prepare without allocations, got %+v", allocs)
	}
	if len(reasons) != 1 || reasons[0].Group != "entitlement" || reasons[0].Code != "insufficient_entitlement" {
		t.Fatalf("want entitlement/insufficient_entitlement, got %v", reasons)
	}
}

func TestWaterfallExpiredPoolsSkippedWithoutGate(t *testing.T) {
	// An expired metered pool is skipped outright — it must not trigger the
	// spend-policy gate for an allocation that never touches it.
	pools := []EntitlementPool{
		pool("paid-expired", 100, true, true),
		pool("free-a", 50, false, false),
	}
	allocs, admitted, reasons := AllocateWaterfall(pools, 50, false)
	if !admitted || len(reasons) != 0 {
		t.Fatalf("expired pool must be skipped silently, got %v %v", admitted, reasons)
	}
	want := []EntitlementAllocation{{PoolID: "free-a", Units: 50}}
	if !reflect.DeepEqual(allocs, want) {
		t.Fatalf("want %+v, got %+v", want, allocs)
	}
}

func TestWaterfallZeroNeedSucceeds(t *testing.T) {
	pools := []EntitlementPool{pool("paid-a", 100, true, false)}
	allocs, admitted, reasons := AllocateWaterfall(pools, 0, false)
	if !admitted || len(allocs) != 0 || len(reasons) != 0 {
		t.Fatalf("need=0 must succeed before any gate, got admitted=%v allocs=%+v reasons=%v", admitted, allocs, reasons)
	}
}

func TestWaterfallNegativeNeedRefused(t *testing.T) {
	_, admitted, reasons := AllocateWaterfall(nil, -1, true)
	if admitted || len(reasons) != 1 || reasons[0].Code != "invalid_quantity" {
		t.Fatalf("negative need must refuse, got %v %v", admitted, reasons)
	}
}
