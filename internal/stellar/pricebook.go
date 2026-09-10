package stellar

import (
	"context"
	"fmt"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// readOnlySourceAccount is the transaction source account used for every
// pure read (simulate-only) call in this package. Its value is arbitrary —
// verified live against soroban-testnet.stellar.org that
// simulateTransaction never checks whether this account exists or is
// funded for a call shaped like this (see pricebook_test.go) — but the
// envelope format requires a well-formed G... strkey, so a fixed one saves
// every caller from inventing their own.
const readOnlySourceAccount = "GAAACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB7JZX"

// PriceBookVersion mirrors the price_book contract's PriceBookVersion
// struct (§4): PriceBookVersion { operator, version, schedule_hash, uri,
// effective_ledger, published_ledger }.
type PriceBookVersion struct {
	Operator        string
	Version         uint32
	ScheduleHash    [32]byte
	URI             string
	EffectiveLedger uint32
	PublishedLedger uint32
}

// PriceBook is a typed binding to a deployed price_book contract instance.
//
// Contract-specific error codes (§4: NotFound, EffectiveInPast,
// EffectiveNotAfter, TimelineFull, UriTooLong) are not decoded into typed
// Go errors here — doing that reliably requires parsing a failed
// invocation's diagnostic events, which this package has not verified
// against a live failure of each kind. Errors from this type carry the
// contract's own message text (via ErrSimulationFailed or
// ErrTransactionFailed); a caller that must distinguish error kinds
// programmatically will need to parse that text itself for now.
type PriceBook struct {
	Client            *Client
	ContractID        string
	NetworkPassphrase string
}

// NewPriceBook returns a binding to the price_book contract at contractID.
func NewPriceBook(client *Client, contractID, networkPassphrase string) *PriceBook {
	return &PriceBook{Client: client, ContractID: contractID, NetworkPassphrase: networkPassphrase}
}

// Latest returns operator's current price book version.
func (p *PriceBook) Latest(ctx context.Context, operator string) (uint32, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return 0, fmt.Errorf("price_book: latest: %w", err)
	}
	result, err := SimulateCall(ctx, p.Client, readOnlySourceAccount, p.ContractID, "latest", []xdr.ScVal{operatorArg})
	if err != nil {
		return 0, fmt.Errorf("price_book: latest: %w", err)
	}
	v, err := DecodeU32(result)
	if err != nil {
		return 0, fmt.Errorf("price_book: latest: decode result: %w", err)
	}
	return v, nil
}

// VersionAt returns which price book version applied at ledger.
func (p *PriceBook) VersionAt(ctx context.Context, operator string, ledger uint32) (uint32, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return 0, fmt.Errorf("price_book: version_at: %w", err)
	}
	args := []xdr.ScVal{operatorArg, ScvU32(ledger)}
	result, err := SimulateCall(ctx, p.Client, readOnlySourceAccount, p.ContractID, "version_at", args)
	if err != nil {
		return 0, fmt.Errorf("price_book: version_at: %w", err)
	}
	v, err := DecodeU32(result)
	if err != nil {
		return 0, fmt.Errorf("price_book: version_at: decode result: %w", err)
	}
	return v, nil
}

// GetVersion returns the full published record for operator's version.
func (p *PriceBook) GetVersion(ctx context.Context, operator string, version uint32) (*PriceBookVersion, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return nil, fmt.Errorf("price_book: get_version: %w", err)
	}
	args := []xdr.ScVal{operatorArg, ScvU32(version)}
	result, err := SimulateCall(ctx, p.Client, readOnlySourceAccount, p.ContractID, "get_version", args)
	if err != nil {
		return nil, fmt.Errorf("price_book: get_version: %w", err)
	}
	pbv, err := decodePriceBookVersion(result)
	if err != nil {
		return nil, fmt.Errorf("price_book: get_version: %w", err)
	}
	return pbv, nil
}

// Publish publishes a new price book version, authorized by signer, who
// acts as both the transaction source account and the operator argument
// (self-authorized: the contract's operator.require_auth() is satisfied by
// signer's own transaction signature — see invoke.go). Returns the newly
// published version number.
func (p *PriceBook) Publish(ctx context.Context, signer *keypair.Full, scheduleHash [32]byte, uri string, effectiveLedger uint32) (uint32, string, error) {
	operatorArg, err := ScvAddress(signer.Address())
	if err != nil {
		return 0, "", fmt.Errorf("price_book: publish: %w", err)
	}
	args := []xdr.ScVal{
		operatorArg,
		ScvBytes(scheduleHash[:]),
		ScvString(uri),
		ScvU32(effectiveLedger),
	}
	result, hash, err := InvokeAndSubmit(ctx, p.Client, p.NetworkPassphrase, signer, p.ContractID, "publish", args)
	if err != nil {
		return 0, hash, fmt.Errorf("price_book: publish: %w", err)
	}
	version, err := DecodeU32(result)
	if err != nil {
		return 0, hash, fmt.Errorf("price_book: publish: decode result: %w", err)
	}
	return version, hash, nil
}

func decodePriceBookVersion(v xdr.ScVal) (*PriceBookVersion, error) {
	m, err := DecodeMap(v)
	if err != nil {
		return nil, fmt.Errorf("decode PriceBookVersion: %w", err)
	}

	get := func(key string) (xdr.ScVal, error) { return MapField(m, key) }

	operatorVal, err := get("operator")
	if err != nil {
		return nil, err
	}
	operator, err := DecodeAddress(operatorVal)
	if err != nil {
		return nil, fmt.Errorf("field operator: %w", err)
	}

	versionVal, err := get("version")
	if err != nil {
		return nil, err
	}
	version, err := DecodeU32(versionVal)
	if err != nil {
		return nil, fmt.Errorf("field version: %w", err)
	}

	scheduleHashVal, err := get("schedule_hash")
	if err != nil {
		return nil, err
	}
	scheduleHashBytes, err := DecodeBytes(scheduleHashVal)
	if err != nil {
		return nil, fmt.Errorf("field schedule_hash: %w", err)
	}
	if len(scheduleHashBytes) != 32 {
		return nil, fmt.Errorf("field schedule_hash: got %d bytes, want 32", len(scheduleHashBytes))
	}
	var scheduleHash [32]byte
	copy(scheduleHash[:], scheduleHashBytes)

	uriVal, err := get("uri")
	if err != nil {
		return nil, err
	}
	uri, err := DecodeString(uriVal)
	if err != nil {
		return nil, fmt.Errorf("field uri: %w", err)
	}

	effectiveLedgerVal, err := get("effective_ledger")
	if err != nil {
		return nil, err
	}
	effectiveLedger, err := DecodeU32(effectiveLedgerVal)
	if err != nil {
		return nil, fmt.Errorf("field effective_ledger: %w", err)
	}

	publishedLedgerVal, err := get("published_ledger")
	if err != nil {
		return nil, err
	}
	publishedLedger, err := DecodeU32(publishedLedgerVal)
	if err != nil {
		return nil, fmt.Errorf("field published_ledger: %w", err)
	}

	return &PriceBookVersion{
		Operator:        operator,
		Version:         version,
		ScheduleHash:    scheduleHash,
		URI:             uri,
		EffectiveLedger: effectiveLedger,
		PublishedLedger: publishedLedger,
	}, nil
}
