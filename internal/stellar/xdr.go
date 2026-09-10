package stellar

import (
	"encoding/base64"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// i128 bounds: [-2^127, 2^127 - 1].
var (
	i128Min = new(big.Int).Lsh(big.NewInt(-1), 127)
	i128Max = new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
)

// ErrWrongScValType is returned by a Decode* function when given an ScVal
// of a different type than expected.
type ErrWrongScValType struct {
	Want xdr.ScValType
	Got  xdr.ScValType
}

func (e *ErrWrongScValType) Error() string {
	return fmt.Sprintf("stellar: expected ScVal type %s, got %s", e.Want, e.Got)
}

// --- encoding: Go values to xdr.ScVal ---

// ScvBool builds ScVal::Bool.
func ScvBool(b bool) xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvBool, B: &b}
}

// ScvVoid builds ScVal::Void.
func ScvVoid() xdr.ScVal {
	return xdr.ScVal{Type: xdr.ScValTypeScvVoid}
}

// ScvU32 builds ScVal::U32.
func ScvU32(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

// ScvI32 builds ScVal::I32.
func ScvI32(v int32) xdr.ScVal {
	i := xdr.Int32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvI32, I32: &i}
}

// ScvU64 builds ScVal::U64.
func ScvU64(v uint64) xdr.ScVal {
	u := xdr.Uint64(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &u}
}

// ScvI64 builds ScVal::I64.
func ScvI64(v int64) xdr.ScVal {
	i := xdr.Int64(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvI64, I64: &i}
}

// ScvBytes builds ScVal::Bytes from b, copying it so later mutation of b
// doesn't change the returned value.
func ScvBytes(b []byte) xdr.ScVal {
	sb := xdr.ScBytes(append([]byte(nil), b...))
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &sb}
}

// ScvSymbol builds ScVal::Symbol. Soroban caps symbols at 32 characters;
// that limit is enforced by the XDR encoder itself, not here.
func ScvSymbol(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

// ScvString builds ScVal::String.
func ScvString(s string) xdr.ScVal {
	str := xdr.ScString(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvString, Str: &str}
}

// ScvI128 builds ScVal::I128 from a signed arbitrary-precision integer,
// two's-complement encoded across the Hi/Lo 64-bit halves as the Soroban
// host expects. Returns an error if v is outside [-2^127, 2^127-1].
func ScvI128(v *big.Int) (xdr.ScVal, error) {
	if v == nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: nil i128 value")
	}
	if v.Cmp(i128Min) < 0 || v.Cmp(i128Max) > 0 {
		return xdr.ScVal{}, fmt.Errorf("stellar: value %s out of i128 range", v)
	}

	unsigned := new(big.Int).Set(v)
	if v.Sign() < 0 {
		modulus := new(big.Int).Lsh(big.NewInt(1), 128)
		unsigned.Add(modulus, v)
	}

	var buf [16]byte
	unsigned.FillBytes(buf[:])
	hi := xdr.Int64(uint64(buf[0])<<56 | uint64(buf[1])<<48 | uint64(buf[2])<<40 | uint64(buf[3])<<32 |
		uint64(buf[4])<<24 | uint64(buf[5])<<16 | uint64(buf[6])<<8 | uint64(buf[7]))
	lo := xdr.Uint64(uint64(buf[8])<<56 | uint64(buf[9])<<48 | uint64(buf[10])<<40 | uint64(buf[11])<<32 |
		uint64(buf[12])<<24 | uint64(buf[13])<<16 | uint64(buf[14])<<8 | uint64(buf[15]))

	parts := xdr.Int128Parts{Hi: hi, Lo: lo}
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}, nil
}

// ScvAddress builds ScVal::Address from a strkey address — either a G...
// account or a C... contract address.
func ScvAddress(address string) (xdr.ScVal, error) {
	addr, err := scAddress(address)
	if err != nil {
		return xdr.ScVal{}, err
	}
	return xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &addr}, nil
}

// scAddress decodes a G... or C... strkey address into an xdr.ScAddress.
func scAddress(address string) (xdr.ScAddress, error) {
	version, raw, err := strkey.DecodeAny(address)
	if err != nil {
		return xdr.ScAddress{}, fmt.Errorf("stellar: decode strkey address %q: %w", address, err)
	}
	if len(raw) != 32 {
		return xdr.ScAddress{}, fmt.Errorf("stellar: decoded address %q is %d bytes, want 32", address, len(raw))
	}
	var raw32 xdr.Uint256
	copy(raw32[:], raw)

	switch version {
	case strkey.VersionByteAccountID:
		accountID := xdr.AccountId(xdr.PublicKey{
			Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
			Ed25519: &raw32,
		})
		return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &accountID}, nil
	case strkey.VersionByteContract:
		hash := xdr.Hash(raw32)
		contractID := xdr.ContractId(hash)
		return xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID}, nil
	default:
		return xdr.ScAddress{}, fmt.Errorf("stellar: address %q is neither an account (G...) nor a contract (C...) strkey", address)
	}
}

// ScvVec builds ScVal::Vec from items, in order.
func ScvVec(items []xdr.ScVal) xdr.ScVal {
	vec := xdr.ScVec(items)
	vecPtr := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &vecPtr}
}

// MapEntry builds one ScMapEntry with a Symbol key — the convention
// soroban-sdk's derive macros use for struct fields, and the one every
// contract binding in this package relies on.
func MapEntry(key string, val xdr.ScVal) xdr.ScMapEntry {
	return xdr.ScMapEntry{Key: ScvSymbol(key), Val: val}
}

// ScvMap builds ScVal::Map from entries, which callers must already have in
// the map's required key order (sorted by key — see internal/merkle's leaf
// encoding for why this matters when the map format is contract-defined).
func ScvMap(entries []xdr.ScMapEntry) xdr.ScVal {
	m := xdr.ScMap(entries)
	mapPtr := &m
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &mapPtr}
}

// --- decoding: xdr.ScVal to Go values ---

// DecodeBool decodes ScVal::Bool.
func DecodeBool(v xdr.ScVal) (bool, error) {
	if v.Type != xdr.ScValTypeScvBool || v.B == nil {
		return false, &ErrWrongScValType{Want: xdr.ScValTypeScvBool, Got: v.Type}
	}
	return bool(*v.B), nil
}

// DecodeU32 decodes ScVal::U32.
func DecodeU32(v xdr.ScVal) (uint32, error) {
	if v.Type != xdr.ScValTypeScvU32 || v.U32 == nil {
		return 0, &ErrWrongScValType{Want: xdr.ScValTypeScvU32, Got: v.Type}
	}
	return uint32(*v.U32), nil
}

// DecodeI32 decodes ScVal::I32.
func DecodeI32(v xdr.ScVal) (int32, error) {
	if v.Type != xdr.ScValTypeScvI32 || v.I32 == nil {
		return 0, &ErrWrongScValType{Want: xdr.ScValTypeScvI32, Got: v.Type}
	}
	return int32(*v.I32), nil
}

// DecodeU64 decodes ScVal::U64.
func DecodeU64(v xdr.ScVal) (uint64, error) {
	if v.Type != xdr.ScValTypeScvU64 || v.U64 == nil {
		return 0, &ErrWrongScValType{Want: xdr.ScValTypeScvU64, Got: v.Type}
	}
	return uint64(*v.U64), nil
}

// DecodeI64 decodes ScVal::I64.
func DecodeI64(v xdr.ScVal) (int64, error) {
	if v.Type != xdr.ScValTypeScvI64 || v.I64 == nil {
		return 0, &ErrWrongScValType{Want: xdr.ScValTypeScvI64, Got: v.Type}
	}
	return int64(*v.I64), nil
}

// DecodeI128 decodes ScVal::I128 into a signed arbitrary-precision integer,
// reversing ScvI128's two's-complement Hi/Lo encoding.
func DecodeI128(v xdr.ScVal) (*big.Int, error) {
	if v.Type != xdr.ScValTypeScvI128 || v.I128 == nil {
		return nil, &ErrWrongScValType{Want: xdr.ScValTypeScvI128, Got: v.Type}
	}
	hi := uint64(v.I128.Hi)
	lo := uint64(v.I128.Lo)

	unsigned := new(big.Int).Lsh(new(big.Int).SetUint64(hi), 64)
	unsigned.Or(unsigned, new(big.Int).SetUint64(lo))

	if v.I128.Hi >= 0 {
		return unsigned, nil
	}
	modulus := new(big.Int).Lsh(big.NewInt(1), 128)
	return new(big.Int).Sub(unsigned, modulus), nil
}

// DecodeBytes decodes ScVal::Bytes.
func DecodeBytes(v xdr.ScVal) ([]byte, error) {
	if v.Type != xdr.ScValTypeScvBytes || v.Bytes == nil {
		return nil, &ErrWrongScValType{Want: xdr.ScValTypeScvBytes, Got: v.Type}
	}
	return append([]byte(nil), (*v.Bytes)...), nil
}

// DecodeSymbol decodes ScVal::Symbol.
func DecodeSymbol(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvSymbol || v.Sym == nil {
		return "", &ErrWrongScValType{Want: xdr.ScValTypeScvSymbol, Got: v.Type}
	}
	return string(*v.Sym), nil
}

// DecodeString decodes ScVal::String.
func DecodeString(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvString || v.Str == nil {
		return "", &ErrWrongScValType{Want: xdr.ScValTypeScvString, Got: v.Type}
	}
	return string(*v.Str), nil
}

// DecodeAddress decodes ScVal::Address back to a strkey address — a G...
// account or C... contract address depending on which it was.
func DecodeAddress(v xdr.ScVal) (string, error) {
	if v.Type != xdr.ScValTypeScvAddress || v.Address == nil {
		return "", &ErrWrongScValType{Want: xdr.ScValTypeScvAddress, Got: v.Type}
	}
	switch v.Address.Type {
	case xdr.ScAddressTypeScAddressTypeAccount:
		if v.Address.AccountId == nil || v.Address.AccountId.Ed25519 == nil {
			return "", fmt.Errorf("stellar: address has account type but no ed25519 key")
		}
		raw := (*v.Address.AccountId.Ed25519)[:]
		return strkey.Encode(strkey.VersionByteAccountID, raw)
	case xdr.ScAddressTypeScAddressTypeContract:
		if v.Address.ContractId == nil {
			return "", fmt.Errorf("stellar: address has contract type but no contract id")
		}
		raw := (*v.Address.ContractId)[:]
		return strkey.Encode(strkey.VersionByteContract, raw)
	default:
		return "", fmt.Errorf("stellar: unsupported ScAddress type %v", v.Address.Type)
	}
}

// DecodeVec decodes ScVal::Vec.
func DecodeVec(v xdr.ScVal) ([]xdr.ScVal, error) {
	if v.Type != xdr.ScValTypeScvVec || v.Vec == nil || *v.Vec == nil {
		return nil, &ErrWrongScValType{Want: xdr.ScValTypeScvVec, Got: v.Type}
	}
	return []xdr.ScVal(**v.Vec), nil
}

// DecodeMap decodes ScVal::Map into a Go map keyed by each entry's Symbol
// key — the convention soroban-sdk uses for struct fields, and the only
// one this package's contract bindings need. An entry whose key isn't a
// Symbol is an error rather than silently dropped.
func DecodeMap(v xdr.ScVal) (map[string]xdr.ScVal, error) {
	if v.Type != xdr.ScValTypeScvMap || v.Map == nil || *v.Map == nil {
		return nil, &ErrWrongScValType{Want: xdr.ScValTypeScvMap, Got: v.Type}
	}
	entries := []xdr.ScMapEntry(**v.Map)
	out := make(map[string]xdr.ScVal, len(entries))
	for _, e := range entries {
		key, err := DecodeSymbol(e.Key)
		if err != nil {
			return nil, fmt.Errorf("stellar: map entry key: %w", err)
		}
		out[key] = e.Val
	}
	return out, nil
}

// --- base64 XDR codec, for RPC request/response payloads ---

// MarshalScValBase64 XDR-encodes v and base64-encodes the result, the
// format every Soroban RPC method that carries an ScVal uses on the wire.
func MarshalScValBase64(v xdr.ScVal) (string, error) {
	b, err := v.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("stellar: marshal scval: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// UnmarshalScValBase64 reverses MarshalScValBase64.
func UnmarshalScValBase64(s string) (xdr.ScVal, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: base64 decode scval: %w", err)
	}
	var v xdr.ScVal
	if err := v.UnmarshalBinary(b); err != nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: unmarshal scval: %w", err)
	}
	return v, nil
}
