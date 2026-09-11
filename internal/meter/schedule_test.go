package meter

import (
	"errors"
	"math/big"
	"strings"
	"testing"
)

func TestParseSchedule_Valid(t *testing.T) {
	raw := []byte(`{"rules": [
		{"method": "get", "path_template": "/v1/items/{id}", "unit_price": "1000"},
		{"method": "POST", "path_template": "/v1/items", "unit_price": "5000"}
	]}`)

	sched, err := ParseSchedule(raw)
	if err != nil {
		t.Fatalf("ParseSchedule returned unexpected error: %v", err)
	}

	if len(sched.Rules()) != 2 {
		t.Fatalf("Rules() returned %d rules, want 2", len(sched.Rules()))
	}

	// Method normalized to uppercase; Lookup is case-insensitive on method
	// regardless of how it was written in the document or queried.
	price, ok := sched.Lookup("GET", "/v1/items/{id}")
	if !ok {
		t.Fatal("Lookup(GET, /v1/items/{id}) not found")
	}
	if price.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Lookup(GET, /v1/items/{id}) = %s, want 1000", price)
	}

	price, ok = sched.Lookup("get", "/v1/items/{id}")
	if !ok || price.Cmp(big.NewInt(1000)) != 0 {
		t.Errorf("Lookup is not case-insensitive on method: ok=%v price=%v", ok, price)
	}

	price, ok = sched.Lookup("POST", "/v1/items")
	if !ok || price.Cmp(big.NewInt(5000)) != 0 {
		t.Errorf("Lookup(POST, /v1/items) = %v, %v, want 5000, true", price, ok)
	}
}

func TestParseSchedule_LookupMissesUnknownEndpoint(t *testing.T) {
	sched, err := ParseSchedule([]byte(`{"rules": [{"method": "GET", "path_template": "/v1/items", "unit_price": "1"}]}`))
	if err != nil {
		t.Fatalf("ParseSchedule returned unexpected error: %v", err)
	}
	if _, ok := sched.Lookup("GET", "/v1/other"); ok {
		t.Error("Lookup found a rule for an endpoint that was never in the schedule")
	}
	if _, ok := sched.Lookup("DELETE", "/v1/items"); ok {
		t.Error("Lookup found a rule for a method that was never in the schedule for this path")
	}
}

func TestParseSchedule_RejectsMalformedJSON(t *testing.T) {
	if _, err := ParseSchedule([]byte(`not json`)); err == nil {
		t.Fatal("ParseSchedule accepted malformed JSON")
	}
}

func TestParseSchedule_RejectsEmptyRules(t *testing.T) {
	_, err := ParseSchedule([]byte(`{"rules": []}`))
	if !errors.Is(err, ErrEmptySchedule) {
		t.Fatalf("ParseSchedule error = %v, want ErrEmptySchedule", err)
	}
}

func TestParseSchedule_RejectsDuplicateRule(t *testing.T) {
	raw := []byte(`{"rules": [
		{"method": "GET", "path_template": "/v1/items", "unit_price": "1"},
		{"method": "get", "path_template": "/v1/items", "unit_price": "2"}
	]}`)
	_, err := ParseSchedule(raw)
	if !errors.Is(err, ErrDuplicateRule) {
		t.Fatalf("ParseSchedule error = %v, want ErrDuplicateRule", err)
	}
}

func TestParseSchedule_RejectsEmptyMethod(t *testing.T) {
	_, err := ParseSchedule([]byte(`{"rules": [{"method": "", "path_template": "/v1/items", "unit_price": "1"}]}`))
	if !errors.Is(err, ErrInvalidMethod) {
		t.Fatalf("ParseSchedule error = %v, want ErrInvalidMethod", err)
	}
}

func TestParseSchedule_RejectsBadPathTemplate(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"missing leading slash", "v1/items"},
		{"whitespace only", "   "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(`{"rules": [{"method": "GET", "path_template": "` + tt.path + `", "unit_price": "1"}]}`)
			_, err := ParseSchedule(raw)
			if !errors.Is(err, ErrInvalidPathTemplate) {
				t.Fatalf("ParseSchedule error = %v, want ErrInvalidPathTemplate", err)
			}
		})
	}
}

func TestParseSchedule_RejectsInvalidUnitPrice(t *testing.T) {
	tests := []struct {
		name  string
		price string
	}{
		{"not a number", "free"},
		{"negative", "-1"},
		{"decimal", "1.5"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := []byte(`{"rules": [{"method": "GET", "path_template": "/v1/items", "unit_price": "` + tt.price + `"}]}`)
			_, err := ParseSchedule(raw)
			if !errors.Is(err, ErrInvalidUnitPrice) {
				t.Fatalf("ParseSchedule error = %v, want ErrInvalidUnitPrice", err)
			}
		})
	}
}

func TestParseSchedule_RejectsUnitPriceOutOfRange(t *testing.T) {
	tooBig := new(big.Int).Add(i128Max, big.NewInt(1))
	raw := []byte(`{"rules": [{"method": "GET", "path_template": "/v1/items", "unit_price": "` + tooBig.String() + `"}]}`)
	_, err := ParseSchedule(raw)
	if !errors.Is(err, ErrUnitPriceOutOfRange) {
		t.Fatalf("ParseSchedule error = %v, want ErrUnitPriceOutOfRange", err)
	}
}

func TestParseSchedule_AcceptsUnitPriceAtI128Max(t *testing.T) {
	raw := []byte(`{"rules": [{"method": "GET", "path_template": "/v1/items", "unit_price": "` + i128Max.String() + `"}]}`)
	sched, err := ParseSchedule(raw)
	if err != nil {
		t.Fatalf("ParseSchedule returned unexpected error at the i128 boundary: %v", err)
	}
	price, _ := sched.Lookup("GET", "/v1/items")
	if price.Cmp(i128Max) != 0 {
		t.Errorf("price = %s, want %s", price, i128Max)
	}
}

func TestScheduleHash_DeterministicAndSensitiveToContent(t *testing.T) {
	a := []byte(`{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"1"}]}`)
	b := []byte(`{"rules":[{"method":"GET","path_template":"/v1/items","unit_price":"2"}]}`)

	h1 := ScheduleHash(a)
	h2 := ScheduleHash(a)
	if h1 != h2 {
		t.Error("ScheduleHash is not deterministic for identical input")
	}

	h3 := ScheduleHash(b)
	if h1 == h3 {
		t.Error("ScheduleHash did not change when the document content changed")
	}
}

func TestSchedule_RulesReturnsDefensiveCopy(t *testing.T) {
	sched, err := ParseSchedule([]byte(`{"rules": [{"method": "GET", "path_template": "/v1/items", "unit_price": "1"}]}`))
	if err != nil {
		t.Fatalf("ParseSchedule returned unexpected error: %v", err)
	}
	rules := sched.Rules()
	rules[0].Method = "MUTATED"

	price, ok := sched.Lookup("GET", "/v1/items")
	if !ok || price.Cmp(big.NewInt(1)) != 0 {
		t.Error("mutating the slice returned by Rules() affected the Schedule's own state")
	}
}

func TestParseSchedule_ErrorNamesTheOffendingRule(t *testing.T) {
	raw := []byte(`{"rules": [
		{"method": "GET", "path_template": "/v1/ok", "unit_price": "1"},
		{"method": "GET", "path_template": "/v1/bad", "unit_price": "not-a-number"}
	]}`)
	_, err := ParseSchedule(raw)
	if err == nil {
		t.Fatal("ParseSchedule returned nil error")
	}
	if !strings.Contains(err.Error(), "rule 1") {
		t.Errorf("error %q does not name rule index 1", err.Error())
	}
}
