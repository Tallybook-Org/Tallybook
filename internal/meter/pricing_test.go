package meter

import (
	"errors"
	"fmt"
	"math/big"
	"testing"
)

func mustSchedule(t *testing.T, raw string) *Schedule {
	t.Helper()
	sched, err := ParseSchedule([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSchedule returned unexpected error: %v", err)
	}
	return sched
}

func TestNewCatalog_RejectsEmpty(t *testing.T) {
	_, err := NewCatalog()
	if !errors.Is(err, ErrEmptyCatalog) {
		t.Fatalf("NewCatalog() error = %v, want ErrEmptyCatalog", err)
	}
}

func TestNewCatalog_RejectsNilSchedule(t *testing.T) {
	_, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 100, Schedule: nil})
	if !errors.Is(err, ErrNilSchedule) {
		t.Fatalf("NewCatalog() error = %v, want ErrNilSchedule", err)
	}
}

func TestNewCatalog_RejectsDuplicateVersion(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"1"}]}`)
	_, err := NewCatalog(
		CatalogVersion{Version: 1, EffectiveLedger: 100, Schedule: sched},
		CatalogVersion{Version: 1, EffectiveLedger: 200, Schedule: sched},
	)
	if !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("NewCatalog() error = %v, want ErrDuplicateVersion", err)
	}
}

func TestNewCatalog_RejectsDuplicateEffectiveLedger(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"1"}]}`)
	_, err := NewCatalog(
		CatalogVersion{Version: 1, EffectiveLedger: 100, Schedule: sched},
		CatalogVersion{Version: 2, EffectiveLedger: 100, Schedule: sched},
	)
	if !errors.Is(err, ErrDuplicateVersion) {
		t.Fatalf("NewCatalog() error = %v, want ErrDuplicateVersion", err)
	}
}

func TestCatalog_VersionAt_PicksTheEffectiveVersion(t *testing.T) {
	schedV1 := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"100"}]}`)
	schedV2 := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"200"}]}`)

	// Constructed out of order deliberately — NewCatalog must sort.
	catalog, err := NewCatalog(
		CatalogVersion{Version: 2, EffectiveLedger: 2000, Schedule: schedV2},
		CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: schedV1},
	)
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}

	v, err := catalog.VersionAt(1500)
	if err != nil {
		t.Fatalf("VersionAt(1500) returned unexpected error: %v", err)
	}
	if v.Version != 1 {
		t.Errorf("VersionAt(1500).Version = %d, want 1", v.Version)
	}

	v, err = catalog.VersionAt(2500)
	if err != nil {
		t.Fatalf("VersionAt(2500) returned unexpected error: %v", err)
	}
	if v.Version != 2 {
		t.Errorf("VersionAt(2500).Version = %d, want 2", v.Version)
	}
}

func TestCatalog_VersionAt_BeforeEarliestReturnsErrNoVersionAt(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"1"}]}`)
	catalog, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: sched})
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}
	if _, err := catalog.VersionAt(999); !errors.Is(err, ErrNoVersionAt) {
		t.Fatalf("VersionAt(999) error = %v, want ErrNoVersionAt", err)
	}
}

func TestPricer_Price_ComputesChargedAmount(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"1500"}]}`)
	catalog, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: sched})
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}
	pricer := NewPricer(catalog)

	priced, err := pricer.Price(1000, "GET", "/v1/items", 3)
	if err != nil {
		t.Fatalf("Price returned unexpected error: %v", err)
	}
	if priced.ChargedAmount.Cmp(big.NewInt(4500)) != 0 {
		t.Errorf("ChargedAmount = %s, want 4500", priced.ChargedAmount)
	}
	if priced.PriceVersion != 1 {
		t.Errorf("PriceVersion = %d, want 1", priced.PriceVersion)
	}
}

func TestPricer_Price_UnknownEndpoint(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"1"}]}`)
	catalog, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: sched})
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}
	pricer := NewPricer(catalog)

	if _, err := pricer.Price(1000, "GET", "/v1/nope", 1); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("Price error = %v, want ErrUnknownEndpoint", err)
	}
}

func TestPricer_Price_NoVersionAt(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"1"}]}`)
	catalog, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: sched})
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}
	pricer := NewPricer(catalog)

	if _, err := pricer.Price(500, "GET", "/v1/items", 1); !errors.Is(err, ErrNoVersionAt) {
		t.Fatalf("Price error = %v, want ErrNoVersionAt", err)
	}
}

// --- version-boundary coverage ---
//
// version_at's contract semantics (§4) are "which schedule applied at a
// ledger" — an inclusive lower bound, exclusive upper bound per version:
// version N applies to every ledger in [EffectiveLedger_N, EffectiveLedger_{N+1}).
// The tests below probe exactly the ledgers where that arithmetic is easiest
// to get off by one: the ledger a new version becomes effective on, the
// ledger immediately before it, and the exact ledger of the very first
// version. This is also the arithmetic anchor's PeriodSpansPriceChange
// check (§4 constraint 1) depends on — a period whose start and end ledger
// resolve to different Catalog versions is exactly the statement the
// contract will reject, so a boundary bug here would surface there too,
// on chain, as a rejected anchor rather than a local test failure.

func threeVersionCatalog(t *testing.T) *Catalog {
	t.Helper()
	schedV1 := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"100"}]}`)
	schedV2 := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"200"}]}`)
	schedV3 := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"300"}]}`)

	catalog, err := NewCatalog(
		CatalogVersion{Version: 1, EffectiveLedger: 1000, Schedule: schedV1},
		CatalogVersion{Version: 2, EffectiveLedger: 2000, Schedule: schedV2},
		CatalogVersion{Version: 3, EffectiveLedger: 3000, Schedule: schedV3},
	)
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}
	return catalog
}

func TestCatalog_VersionAt_ExactBoundaryLedgersBelongToTheNewVersion(t *testing.T) {
	catalog := threeVersionCatalog(t)

	tests := []struct {
		ledger      uint32
		wantVersion uint32
	}{
		{999, 1},    // just before v1 even starts: still an error case, checked separately
		{1000, 1},   // v1's own effective ledger: belongs to v1
		{1999, 1},   // last ledger before v2 takes effect: still v1
		{2000, 2},   // v2's effective ledger: belongs to v2, not v1
		{2001, 2},   // just after v2 takes effect: still v2
		{2999, 2},   // last ledger before v3 takes effect: still v2
		{3000, 3},   // v3's effective ledger: belongs to v3
		{999999, 3}, // far beyond the last version: still v3, the latest published
	}

	for _, tt := range tests {
		if tt.ledger == 999 {
			continue // covered by TestCatalog_VersionAt_JustBeforeEarliestVersionErrors below with this exact catalog
		}
		t.Run(fmt.Sprintf("ledger_%d", tt.ledger), func(t *testing.T) {
			v, err := catalog.VersionAt(tt.ledger)
			if err != nil {
				t.Fatalf("VersionAt(%d) returned unexpected error: %v", tt.ledger, err)
			}
			if v.Version != tt.wantVersion {
				t.Errorf("VersionAt(%d).Version = %d, want %d", tt.ledger, v.Version, tt.wantVersion)
			}
		})
	}
}

func TestCatalog_VersionAt_JustBeforeEarliestVersionErrors(t *testing.T) {
	catalog := threeVersionCatalog(t)
	if _, err := catalog.VersionAt(999); !errors.Is(err, ErrNoVersionAt) {
		t.Fatalf("VersionAt(999) error = %v, want ErrNoVersionAt", err)
	}
}

func TestCatalog_VersionAt_SingleVersionAtItsOwnEffectiveLedger(t *testing.T) {
	sched := mustSchedule(t, `{"rules":[{"method":"GET","path_template":"/v1/x","unit_price":"1"}]}`)
	catalog, err := NewCatalog(CatalogVersion{Version: 1, EffectiveLedger: 500, Schedule: sched})
	if err != nil {
		t.Fatalf("NewCatalog returned unexpected error: %v", err)
	}

	v, err := catalog.VersionAt(500)
	if err != nil {
		t.Fatalf("VersionAt(500) returned unexpected error: %v", err)
	}
	if v.Version != 1 {
		t.Errorf("VersionAt(500).Version = %d, want 1", v.Version)
	}

	if _, err := catalog.VersionAt(499); !errors.Is(err, ErrNoVersionAt) {
		t.Fatalf("VersionAt(499) error = %v, want ErrNoVersionAt", err)
	}
}

// TestPricer_Price_ChargesTheBoundaryVersionsDifferentAmounts confirms the
// boundary isn't just picking the right version number — it's actually
// pricing against that version's own distinct schedule, not accidentally
// reusing whichever schedule happened to be loaded first.
func TestPricer_Price_ChargesTheBoundaryVersionsDifferentAmounts(t *testing.T) {
	catalog := threeVersionCatalog(t)
	pricer := NewPricer(catalog)

	beforeBoundary, err := pricer.Price(1999, "GET", "/v1/items", 1)
	if err != nil {
		t.Fatalf("Price(1999) returned unexpected error: %v", err)
	}
	if beforeBoundary.PriceVersion != 1 || beforeBoundary.ChargedAmount.Cmp(big.NewInt(100)) != 0 {
		t.Errorf("Price(1999) = version %d, amount %s, want version 1, amount 100",
			beforeBoundary.PriceVersion, beforeBoundary.ChargedAmount)
	}

	atBoundary, err := pricer.Price(2000, "GET", "/v1/items", 1)
	if err != nil {
		t.Fatalf("Price(2000) returned unexpected error: %v", err)
	}
	if atBoundary.PriceVersion != 2 || atBoundary.ChargedAmount.Cmp(big.NewInt(200)) != 0 {
		t.Errorf("Price(2000) = version %d, amount %s, want version 2, amount 200",
			atBoundary.PriceVersion, atBoundary.ChargedAmount)
	}
}

// TestCatalog_VersionAt_DetectsAPeriodSpanningAPriceChange exercises the
// exact scenario CLAUDE.md §4 constraint 1 warns about: a billing period
// whose start and end ledger straddle a price change resolves to two
// different versions here, locally — the same mismatch
// statement_registry.anchor's PeriodSpansPriceChange check would reject on
// chain. This package does not close periods on that boundary itself (that
// is meter's period-closing responsibility, built later); this test only
// confirms the version-resolution arithmetic a period-closer would depend
// on is correct at the boundary.
func TestCatalog_VersionAt_DetectsAPeriodSpanningAPriceChange(t *testing.T) {
	catalog := threeVersionCatalog(t)

	periodStart := uint32(1500)
	periodEnd := uint32(2500)

	startVersion, err := catalog.VersionAt(periodStart)
	if err != nil {
		t.Fatalf("VersionAt(periodStart) returned unexpected error: %v", err)
	}
	endVersion, err := catalog.VersionAt(periodEnd)
	if err != nil {
		t.Fatalf("VersionAt(periodEnd) returned unexpected error: %v", err)
	}
	if startVersion.Version == endVersion.Version {
		t.Fatalf("expected periodStart and periodEnd to resolve to different versions, both resolved to %d",
			startVersion.Version)
	}

	// A period entirely within one version's range must not trip the same
	// check — this is the non-spanning control case.
	sameStart, err := catalog.VersionAt(1100)
	if err != nil {
		t.Fatalf("VersionAt(1100) returned unexpected error: %v", err)
	}
	sameEnd, err := catalog.VersionAt(1900)
	if err != nil {
		t.Fatalf("VersionAt(1900) returned unexpected error: %v", err)
	}
	if sameStart.Version != sameEnd.Version {
		t.Errorf("period [1100, 1900] should resolve to one version, got %d and %d",
			sameStart.Version, sameEnd.Version)
	}
}
