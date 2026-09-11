package meter

import (
	"errors"
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
