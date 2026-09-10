package stellar

import (
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

func TestScvU32_RoundTrip(t *testing.T) {
	for _, v := range []uint32{0, 1, 42, 1 << 31, 1<<32 - 1} {
		got, err := DecodeU32(ScvU32(v))
		if err != nil {
			t.Fatalf("DecodeU32(ScvU32(%d)) returned unexpected error: %v", v, err)
		}
		if got != v {
			t.Errorf("round trip %d => %d", v, got)
		}
	}
}

func TestScvU64_RoundTrip(t *testing.T) {
	for _, v := range []uint64{0, 1, 1 << 63, 1<<64 - 1} {
		got, err := DecodeU64(ScvU64(v))
		if err != nil {
			t.Fatalf("DecodeU64(ScvU64(%d)) returned unexpected error: %v", v, err)
		}
		if got != v {
			t.Errorf("round trip %d => %d", v, got)
		}
	}
}

func TestScvI64_RoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 1<<63 - 1, -(1 << 62)} {
		got, err := DecodeI64(ScvI64(v))
		if err != nil {
			t.Fatalf("DecodeI64(ScvI64(%d)) returned unexpected error: %v", v, err)
		}
		if got != v {
			t.Errorf("round trip %d => %d", v, got)
		}
	}
}

func TestScvI32_RoundTrip(t *testing.T) {
	for _, v := range []int32{0, 1, -1, 1<<31 - 1, -(1 << 30)} {
		got, err := DecodeI32(ScvI32(v))
		if err != nil {
			t.Fatalf("DecodeI32(ScvI32(%d)) returned unexpected error: %v", v, err)
		}
		if got != v {
			t.Errorf("round trip %d => %d", v, got)
		}
	}
}

func TestScvBool_RoundTrip(t *testing.T) {
	for _, v := range []bool{true, false} {
		got, err := DecodeBool(ScvBool(v))
		if err != nil {
			t.Fatalf("DecodeBool returned unexpected error: %v", err)
		}
		if got != v {
			t.Errorf("round trip %v => %v", v, got)
		}
	}
}

func TestScvBytes_RoundTrip(t *testing.T) {
	tests := [][]byte{nil, {}, {0x01}, make([]byte, 32)}
	for i := range tests[3] {
		tests[3][i] = byte(i)
	}
	for _, v := range tests {
		got, err := DecodeBytes(ScvBytes(v))
		if err != nil {
			t.Fatalf("DecodeBytes returned unexpected error: %v", err)
		}
		if len(got) != len(v) {
			t.Errorf("round trip len %d => len %d", len(v), len(got))
			continue
		}
		for i := range v {
			if got[i] != v[i] {
				t.Errorf("round trip byte %d: %x => %x", i, v, got)
				break
			}
		}
	}
}

func TestScvBytes_CopiesInput(t *testing.T) {
	b := []byte{1, 2, 3}
	v := ScvBytes(b)
	b[0] = 0xFF
	got, err := DecodeBytes(v)
	if err != nil {
		t.Fatalf("DecodeBytes returned unexpected error: %v", err)
	}
	if got[0] != 1 {
		t.Error("ScvBytes did not copy its input — mutating the source slice changed the encoded value")
	}
}

func TestScvSymbol_RoundTrip(t *testing.T) {
	for _, v := range []string{"", "a", "amount", "price_v", "ABCDEFGHIJKLMNOPQRSTUVWXYZ012345"} {
		got, err := DecodeSymbol(ScvSymbol(v))
		if err != nil {
			t.Fatalf("DecodeSymbol returned unexpected error: %v", err)
		}
		if got != v {
			t.Errorf("round trip %q => %q", v, got)
		}
	}
}

func TestScvString_RoundTrip(t *testing.T) {
	for _, v := range []string{"", "hello world", "https://example.com/schedule.json"} {
		got, err := DecodeString(ScvString(v))
		if err != nil {
			t.Fatalf("DecodeString returned unexpected error: %v", err)
		}
		if got != v {
			t.Errorf("round trip %q => %q", v, got)
		}
	}
}

func TestScvI128_RoundTrip(t *testing.T) {
	tests := []*big.Int{
		big.NewInt(0),
		big.NewInt(1),
		big.NewInt(-1),
		big.NewInt(123456789),
		i128Min,
		i128Max,
		new(big.Int).Add(i128Min, big.NewInt(1)),
		new(big.Int).Sub(i128Max, big.NewInt(1)),
	}
	for _, v := range tests {
		scv, err := ScvI128(v)
		if err != nil {
			t.Fatalf("ScvI128(%s) returned unexpected error: %v", v, err)
		}
		got, err := DecodeI128(scv)
		if err != nil {
			t.Fatalf("DecodeI128 returned unexpected error: %v", err)
		}
		if got.Cmp(v) != 0 {
			t.Errorf("round trip %s => %s", v, got)
		}
	}
}

func TestScvI128_RejectsOutOfRange(t *testing.T) {
	tooLarge := new(big.Int).Add(i128Max, big.NewInt(1))
	tooSmall := new(big.Int).Sub(i128Min, big.NewInt(1))
	for _, v := range []*big.Int{tooLarge, tooSmall, nil} {
		if _, err := ScvI128(v); err == nil {
			t.Errorf("ScvI128(%v) returned nil error, want out-of-range error", v)
		}
	}
}

func TestScvAddress_RoundTrip_Account(t *testing.T) {
	// The strkey encoding of the raw ed25519 key 0x00..0x1f — see
	// internal/merkle/leaf_test.go, same derivation.
	addr := "GAAACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB7JZX"
	scv, err := ScvAddress(addr)
	if err != nil {
		t.Fatalf("ScvAddress returned unexpected error: %v", err)
	}
	got, err := DecodeAddress(scv)
	if err != nil {
		t.Fatalf("DecodeAddress returned unexpected error: %v", err)
	}
	if got != addr {
		t.Errorf("round trip %q => %q", addr, got)
	}
}

func TestScvAddress_RoundTrip_Contract(t *testing.T) {
	// A real deployed contract address (statement_registry, testnet — see
	// CLAUDE.md §4), to exercise the C... branch against a genuine strkey.
	addr := "CB75TTWGP3TLKEDGA2WOLEVCNKLUX6X5KS47GMWGBHUAVES7J55LY25M"
	scv, err := ScvAddress(addr)
	if err != nil {
		t.Fatalf("ScvAddress returned unexpected error: %v", err)
	}
	got, err := DecodeAddress(scv)
	if err != nil {
		t.Fatalf("DecodeAddress returned unexpected error: %v", err)
	}
	if got != addr {
		t.Errorf("round trip %q => %q", addr, got)
	}
}

func TestScvAddress_RejectsInvalid(t *testing.T) {
	for _, addr := range []string{"", "not-a-strkey", "S" + strings.Repeat("A", 55)} {
		if _, err := ScvAddress(addr); err == nil {
			t.Errorf("ScvAddress(%q) returned nil error", addr)
		}
	}
}

func TestScvVec_RoundTrip(t *testing.T) {
	items := []xdr.ScVal{ScvU32(1), ScvSymbol("two"), ScvBool(true)}
	scv := ScvVec(items)
	got, err := DecodeVec(scv)
	if err != nil {
		t.Fatalf("DecodeVec returned unexpected error: %v", err)
	}
	if len(got) != len(items) {
		t.Fatalf("got %d items, want %d", len(got), len(items))
	}
	if v, err := DecodeU32(got[0]); err != nil || v != 1 {
		t.Errorf("items[0] = %v, %v", v, err)
	}
	if v, err := DecodeSymbol(got[1]); err != nil || v != "two" {
		t.Errorf("items[1] = %v, %v", v, err)
	}
	if v, err := DecodeBool(got[2]); err != nil || v != true {
		t.Errorf("items[2] = %v, %v", v, err)
	}
}

func TestScvVec_Empty(t *testing.T) {
	got, err := DecodeVec(ScvVec(nil))
	if err != nil {
		t.Fatalf("DecodeVec returned unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d items, want 0", len(got))
	}
}

func TestScvMap_RoundTrip(t *testing.T) {
	entries := []xdr.ScMapEntry{
		MapEntry("amount", ScvU64(100)),
		MapEntry("consumer", ScvSymbol("placeholder")),
	}
	scv := ScvMap(entries)
	got, err := DecodeMap(scv)
	if err != nil {
		t.Fatalf("DecodeMap returned unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d entries, want 2", len(got))
	}
	if v, err := DecodeU64(got["amount"]); err != nil || v != 100 {
		t.Errorf("map[amount] = %v, %v", v, err)
	}
	if v, err := DecodeSymbol(got["consumer"]); err != nil || v != "placeholder" {
		t.Errorf("map[consumer] = %v, %v", v, err)
	}
}

func TestDecode_WrongTypeReturnsTypedError(t *testing.T) {
	_, err := DecodeU32(ScvBool(true))
	if err == nil {
		t.Fatal("DecodeU32 returned nil error for a Bool value")
	}
	var typeErr *ErrWrongScValType
	if !errors.As(err, &typeErr) {
		t.Errorf("error %v is not *ErrWrongScValType", err)
	}
}

func TestMarshalScValBase64_RoundTrip(t *testing.T) {
	scv := ScvU32(42)
	encoded, err := MarshalScValBase64(scv)
	if err != nil {
		t.Fatalf("MarshalScValBase64 returned unexpected error: %v", err)
	}
	decoded, err := UnmarshalScValBase64(encoded)
	if err != nil {
		t.Fatalf("UnmarshalScValBase64 returned unexpected error: %v", err)
	}
	got, err := DecodeU32(decoded)
	if err != nil || got != 42 {
		t.Errorf("round trip via base64: got %d, %v", got, err)
	}
}

func TestUnmarshalScValBase64_RejectsGarbage(t *testing.T) {
	if _, err := UnmarshalScValBase64("not-valid-base64!!"); err == nil {
		t.Error("UnmarshalScValBase64 returned nil error for invalid base64")
	}
	if _, err := UnmarshalScValBase64("AAAA"); err == nil {
		t.Error("UnmarshalScValBase64 returned nil error for valid base64 that isn't a complete ScVal")
	}
}

// TestDecode_AgainstRealOnChainEvent decodes the exact base64 ScVal blobs
// captured live in testdata/stellar/get_events.json — a real Symbol
// ("fee"), a real Address, and a real I128 amount from a genuine testnet
// event — rather than only synthetic values this package generated itself.
func TestDecode_AgainstRealOnChainEvent(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "stellar", "get_events.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc struct {
		Result struct {
			Events []struct {
				Topic []string `json:"topic"`
				Value string   `json:"value"`
			} `json:"events"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(doc.Result.Events) == 0 {
		t.Fatal("fixture has no events")
	}
	event := doc.Result.Events[0]
	if len(event.Topic) < 2 {
		t.Fatalf("event has %d topics, want at least 2", len(event.Topic))
	}

	topic0, err := UnmarshalScValBase64(event.Topic[0])
	if err != nil {
		t.Fatalf("decode topic[0]: %v", err)
	}
	sym, err := DecodeSymbol(topic0)
	if err != nil {
		t.Fatalf("DecodeSymbol(topic[0]): %v", err)
	}
	if sym != "fee" {
		t.Errorf("topic[0] = %q, want %q", sym, "fee")
	}

	topic1, err := UnmarshalScValBase64(event.Topic[1])
	if err != nil {
		t.Fatalf("decode topic[1]: %v", err)
	}
	addr, err := DecodeAddress(topic1)
	if err != nil {
		t.Fatalf("DecodeAddress(topic[1]): %v", err)
	}
	if !strings.HasPrefix(addr, "G") || len(addr) != 56 {
		t.Errorf("topic[1] decoded to %q, doesn't look like a G... account address", addr)
	}

	value, err := UnmarshalScValBase64(event.Value)
	if err != nil {
		t.Fatalf("decode value: %v", err)
	}
	amount, err := DecodeI128(value)
	if err != nil {
		t.Fatalf("DecodeI128(value): %v", err)
	}
	if amount.Sign() < 0 {
		t.Errorf("amount = %s, want non-negative (this is a fee charge)", amount)
	}
}
