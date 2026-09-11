package store

import (
	"context"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/Tallybook-Org/tallybook/internal/httpapi"
)

// Every test in this file runs against a real Postgres (via testPool, in
// migrate_test.go) with the real migrations applied — an integration test
// (§8), not a mock. RequestStore's job is exactly the SQL and type mapping
// (pgtype.Numeric for charged_amount, NULL-vs-empty-string for channel)
// that only a real database round trip can catch.

func TestRequestStore_RecordRequest_InsertsRow(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned unexpected error: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}

	store := NewRequestStore(pool)

	var requestID [32]byte
	copy(requestID[:], mustHexBytes(t, "aa"))
	var endpointHash [32]byte
	copy(endpointHash[:], mustHexBytes(t, "bb"))

	req := httpapi.RecordedRequest{
		RequestID:      requestID,
		Operator:       "GDOLCHAOYP63BEHGAUJJS5IVQNUXLPBWCHO2HZRBTZRU52XBMW2TRJLM",
		Consumer:       "GAJ46LDZSSYAB4YY6VMPM763P652ZROT7TBG3YYE3BZBAUZDXYDDK6OY",
		EndpointHash:   endpointHash,
		Method:         "GET",
		PathTemplate:   "/v1/items/{id}",
		UnitCount:      3,
		PriceVersion:   1,
		ChargedAmount:  big.NewInt(4500),
		Protocol:       httpapi.ProtocolX402,
		Channel:        "",
		ObservedLedger: 123456,
	}

	if err := store.RecordRequest(ctx, req); err != nil {
		t.Fatalf("RecordRequest returned unexpected error: %v", err)
	}

	var (
		gotOperator       string
		gotConsumer       string
		gotMethod         string
		gotPathTemplate   string
		gotUnitCount      int64
		gotPriceVersion   int32
		gotChargedAmount  string
		gotProtocol       string
		gotChannel        *string
		gotObservedLedger int32
		gotRequestID      []byte
		gotEndpointHash   []byte
	)
	err = pool.QueryRow(ctx, `
		SELECT request_id, operator, consumer, endpoint_hash, method, path_template,
		       unit_count, price_version, charged_amount::text, protocol, channel, observed_ledger
		FROM requests WHERE request_id = $1`, req.RequestID[:],
	).Scan(&gotRequestID, &gotOperator, &gotConsumer, &gotEndpointHash, &gotMethod, &gotPathTemplate,
		&gotUnitCount, &gotPriceVersion, &gotChargedAmount, &gotProtocol, &gotChannel, &gotObservedLedger)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}

	if hex.EncodeToString(gotRequestID) != hex.EncodeToString(req.RequestID[:]) {
		t.Errorf("request_id = %x, want %x", gotRequestID, req.RequestID)
	}
	if gotOperator != req.Operator {
		t.Errorf("operator = %q, want %q", gotOperator, req.Operator)
	}
	if gotConsumer != req.Consumer {
		t.Errorf("consumer = %q, want %q", gotConsumer, req.Consumer)
	}
	if hex.EncodeToString(gotEndpointHash) != hex.EncodeToString(req.EndpointHash[:]) {
		t.Errorf("endpoint_hash = %x, want %x", gotEndpointHash, req.EndpointHash)
	}
	if gotMethod != req.Method {
		t.Errorf("method = %q, want %q", gotMethod, req.Method)
	}
	if gotPathTemplate != req.PathTemplate {
		t.Errorf("path_template = %q, want %q", gotPathTemplate, req.PathTemplate)
	}
	if gotUnitCount != int64(req.UnitCount) {
		t.Errorf("unit_count = %d, want %d", gotUnitCount, req.UnitCount)
	}
	if gotPriceVersion != int32(req.PriceVersion) {
		t.Errorf("price_version = %d, want %d", gotPriceVersion, req.PriceVersion)
	}
	if gotChargedAmount != req.ChargedAmount.String() {
		t.Errorf("charged_amount = %q, want %q", gotChargedAmount, req.ChargedAmount.String())
	}
	if gotProtocol != req.Protocol {
		t.Errorf("protocol = %q, want %q", gotProtocol, req.Protocol)
	}
	if gotChannel != nil {
		t.Errorf("channel = %v, want nil (NULL) for x402", *gotChannel)
	}
	if gotObservedLedger != int32(req.ObservedLedger) {
		t.Errorf("observed_ledger = %d, want %d", gotObservedLedger, req.ObservedLedger)
	}
}

func TestRequestStore_RecordRequest_MPPSessionChannelRoundTrips(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned unexpected error: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}

	store := NewRequestStore(pool)

	var requestID [32]byte
	copy(requestID[:], mustHexBytes(t, "cc"))
	var endpointHash [32]byte
	copy(endpointHash[:], mustHexBytes(t, "dd"))

	req := httpapi.RecordedRequest{
		RequestID:      requestID,
		Operator:       "GDOLCHAOYP63BEHGAUJJS5IVQNUXLPBWCHO2HZRBTZRU52XBMW2TRJLM",
		Consumer:       "GAJ46LDZSSYAB4YY6VMPM763P652ZROT7TBG3YYE3BZBAUZDXYDDK6OY",
		EndpointHash:   endpointHash,
		Method:         "POST",
		PathTemplate:   "/v1/session",
		UnitCount:      1,
		PriceVersion:   2,
		ChargedAmount:  big.NewInt(0),
		Protocol:       httpapi.ProtocolMPPSession,
		Channel:        "CB2IEP4SQ2GC5747HFHNMXEYWEULC5Z5TTTLET2QA4CAA5SYCWAXFKAW",
		ObservedLedger: 1,
	}

	if err := store.RecordRequest(ctx, req); err != nil {
		t.Fatalf("RecordRequest returned unexpected error: %v", err)
	}

	var gotChannel *string
	if err := pool.QueryRow(ctx, `SELECT channel FROM requests WHERE request_id = $1`, req.RequestID[:]).
		Scan(&gotChannel); err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if gotChannel == nil || *gotChannel != req.Channel {
		t.Errorf("channel = %v, want %q", gotChannel, req.Channel)
	}
}

func TestRequestStore_RecordRequest_DuplicateRequestIDFails(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned unexpected error: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}

	store := NewRequestStore(pool)

	var requestID [32]byte
	copy(requestID[:], mustHexBytes(t, "ee"))
	var endpointHash [32]byte
	copy(endpointHash[:], mustHexBytes(t, "ff"))

	req := httpapi.RecordedRequest{
		RequestID:      requestID,
		Operator:       "GDOLCHAOYP63BEHGAUJJS5IVQNUXLPBWCHO2HZRBTZRU52XBMW2TRJLM",
		Consumer:       "GAJ46LDZSSYAB4YY6VMPM763P652ZROT7TBG3YYE3BZBAUZDXYDDK6OY",
		EndpointHash:   endpointHash,
		Method:         "GET",
		PathTemplate:   "/v1/items",
		UnitCount:      1,
		PriceVersion:   1,
		ChargedAmount:  big.NewInt(100),
		Protocol:       httpapi.ProtocolX402,
		ObservedLedger: 1,
	}

	if err := store.RecordRequest(ctx, req); err != nil {
		t.Fatalf("first RecordRequest returned unexpected error: %v", err)
	}
	if err := store.RecordRequest(ctx, req); err == nil {
		t.Fatal("second RecordRequest with the same RequestID returned nil error, want a unique-violation error")
	}
}

func TestRequestStore_RecordRequest_RejectsNilChargedAmount(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned unexpected error: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}

	store := NewRequestStore(pool)
	req := httpapi.RecordedRequest{
		Operator:      "GDOLCHAOYP63BEHGAUJJS5IVQNUXLPBWCHO2HZRBTZRU52XBMW2TRJLM",
		Consumer:      "GAJ46LDZSSYAB4YY6VMPM763P652ZROT7TBG3YYE3BZBAUZDXYDDK6OY",
		Method:        "GET",
		PathTemplate:  "/v1/items",
		Protocol:      httpapi.ProtocolX402,
		ChargedAmount: nil,
	}
	if err := store.RecordRequest(ctx, req); err == nil {
		t.Fatal("RecordRequest accepted a nil ChargedAmount")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM requests`).Scan(&count); err != nil {
		t.Fatalf("count requests: %v", err)
	}
	if count != 0 {
		t.Errorf("requests table has %d rows, want 0 — a nil-amount request should never reach the database", count)
	}
}

func mustHexBytes(t *testing.T, suffix string) []byte {
	t.Helper()
	// Pads suffix out to 32 bytes (64 hex characters) with zeros so each
	// test gets a distinct, valid-length identifier without spelling out 64
	// hex characters by hand.
	padded := suffix + strings.Repeat("0", 64-len(suffix))
	b, err := hex.DecodeString(padded)
	if err != nil {
		t.Fatalf("decode hex fixture: %v", err)
	}
	return b
}
