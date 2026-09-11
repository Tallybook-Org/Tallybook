package stellar

import (
	"context"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Protocol mirrors the statement_registry contract's Protocol enum (§4):
// a unit-variant enum, wire-encoded as a one-element Vec holding the
// variant's Symbol — soroban_sdk's convention for every #[contracttype]
// enum, confirmed live against a real anchored statement (see
// registry_test.go).
type Protocol string

// Protocol values, matching the contract's variant names exactly — the
// wire format is case-sensitive.
const (
	ProtocolX402       Protocol = "X402"
	ProtocolMppCharge  Protocol = "MppCharge"
	ProtocolMppSession Protocol = "MppSession"
)

// Status mirrors the statement_registry contract's Status enum (§4). Set
// by the contract; never passed as an argument.
type Status string

// Status values, matching the contract's variant names exactly.
const (
	StatusAnchored Status = "Anchored"
	StatusDisputed Status = "Disputed"
	StatusResolved Status = "Resolved"
)

// Statement mirrors the statement_registry contract's Statement struct
// (§4). Channel is nil unless Protocol is ProtocolMppSession.
type Statement struct {
	Operator         string
	Consumer         string
	PeriodStart      uint32
	PeriodEnd        uint32
	UsageRoot        [32]byte
	RequestCount     uint64
	Token            string
	AmountBilled     *big.Int
	AmountSettled    *big.Int
	PriceBookVersion uint32
	Protocol         Protocol
	Channel          *string
	AnchoredLedger   uint32
	Status           Status
}

// Dispute mirrors the statement_registry contract's Dispute struct (§4).
// ResolutionHash and ResolvedLedger are nil until resolved.
type Dispute struct {
	Consumer       string
	ReasonHash     [32]byte
	OpenedLedger   uint32
	ResolutionHash *[32]byte
	AmountCredited *big.Int
	ResolvedLedger *uint32
}

// StatementRegistry is a typed binding to a deployed statement_registry
// contract instance.
//
// ResolveDispute needs the consumer's own authorization alongside the
// operator's — see its doc comment. Every other contract-specific error
// code beyond what registry_test.go's live captures happened to exercise
// is not decoded into a typed Go error, for the same reason noted in
// pricebook.go: reliably doing that needs a verified live failure of each
// kind, which this work did not produce for all of them.
type StatementRegistry struct {
	Client            *Client
	ContractID        string
	NetworkPassphrase string
}

// NewStatementRegistry returns a binding to the statement_registry
// contract at contractID.
func NewStatementRegistry(client *Client, contractID, networkPassphrase string) *StatementRegistry {
	return &StatementRegistry{Client: client, ContractID: contractID, NetworkPassphrase: networkPassphrase}
}

// Anchor anchors a new statement, authorized by signer as the operator.
// Returns the newly assigned sequence number.
func (r *StatementRegistry) Anchor(
	ctx context.Context,
	signer *keypair.Full,
	consumer string,
	periodStart, periodEnd uint32,
	usageRoot [32]byte,
	requestCount uint64,
	token string,
	amountBilled, amountSettled *big.Int,
	priceBookVersion uint32,
	protocol Protocol,
	channel *string,
) (uint64, string, error) {
	operatorArg, err := ScvAddress(signer.Address())
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: %w", err)
	}
	consumerArg, err := ScvAddress(consumer)
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: %w", err)
	}
	tokenArg, err := ScvAddress(token)
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: %w", err)
	}
	billedArg, err := ScvI128(amountBilled)
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: amount_billed: %w", err)
	}
	settledArg, err := ScvI128(amountSettled)
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: amount_settled: %w", err)
	}
	channelArg, err := scvOptionalAddress(channel)
	if err != nil {
		return 0, "", fmt.Errorf("statement_registry: anchor: channel: %w", err)
	}

	args := []xdr.ScVal{
		operatorArg,
		consumerArg,
		ScvU32(periodStart),
		ScvU32(periodEnd),
		ScvBytes(usageRoot[:]),
		ScvU64(requestCount),
		tokenArg,
		billedArg,
		settledArg,
		ScvU32(priceBookVersion),
		scvProtocol(protocol),
		channelArg,
	}
	result, hash, err := InvokeAndSubmit(ctx, r.Client, r.NetworkPassphrase, signer, r.ContractID, "anchor", args)
	if err != nil {
		return 0, hash, fmt.Errorf("statement_registry: anchor: %w", err)
	}
	seq, err := DecodeU64(result)
	if err != nil {
		return 0, hash, fmt.Errorf("statement_registry: anchor: decode result: %w", err)
	}
	return seq, hash, nil
}

// VerifyUsage checks proof against the anchored statement's usage_root.
func (r *StatementRegistry) VerifyUsage(ctx context.Context, operator string, seq uint64, leaf [32]byte, proof [][32]byte) (bool, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return false, fmt.Errorf("statement_registry: verify_usage: %w", err)
	}
	proofItems := make([]xdr.ScVal, len(proof))
	for i, p := range proof {
		proofItems[i] = ScvBytes(p[:])
	}
	args := []xdr.ScVal{operatorArg, ScvU64(seq), ScvBytes(leaf[:]), ScvVec(proofItems)}
	result, err := SimulateCall(ctx, r.Client, readOnlySourceAccount, r.ContractID, "verify_usage", args)
	if err != nil {
		return false, fmt.Errorf("statement_registry: verify_usage: %w", err)
	}
	ok, err := DecodeBool(result)
	if err != nil {
		return false, fmt.Errorf("statement_registry: verify_usage: decode result: %w", err)
	}
	return ok, nil
}

// OpenDispute opens a dispute against seq, authorized by signer as the
// consumer named in the statement (verified live: open_dispute requires
// the consumer's own authorization, not the operator's).
func (r *StatementRegistry) OpenDispute(ctx context.Context, signer *keypair.Full, operator string, seq uint64, reasonHash [32]byte) (string, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return "", fmt.Errorf("statement_registry: open_dispute: %w", err)
	}
	consumerArg, err := ScvAddress(signer.Address())
	if err != nil {
		return "", fmt.Errorf("statement_registry: open_dispute: %w", err)
	}
	args := []xdr.ScVal{operatorArg, ScvU64(seq), consumerArg, ScvBytes(reasonHash[:])}
	_, hash, err := InvokeAndSubmit(ctx, r.Client, r.NetworkPassphrase, signer, r.ContractID, "open_dispute", args)
	if err != nil {
		return hash, fmt.Errorf("statement_registry: open_dispute: %w", err)
	}
	return hash, nil
}

// ResolveDisputeArgs builds resolve_dispute's argument vector for operator,
// seq, resolutionHash, and amountCredited. Exported so a party authorizing
// the call via AuthorizeInvocation — the consumer, who is not the caller
// of ResolveDispute — builds byte-for-byte the same arguments the eventual
// ResolveDispute call will use; a mismatch here would make the consumer's
// signature invalid for the call it's actually meant to authorize.
func ResolveDisputeArgs(operator string, seq uint64, resolutionHash [32]byte, amountCredited *big.Int) ([]xdr.ScVal, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: resolve_dispute args: %w", err)
	}
	creditedArg, err := ScvI128(amountCredited)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: resolve_dispute args: amount_credited: %w", err)
	}
	return []xdr.ScVal{operatorArg, ScvU64(seq), ScvBytes(resolutionHash[:]), creditedArg}, nil
}

// ResolveDispute resolves seq's dispute, authorized by signer as the
// operator and by consumerAuth as the consumer.
//
// The two parties are not assumed to share a process: the operator's
// collector holds the operator's key, not the consumer's. consumerAuth is
// built separately — wherever the consumer's key actually is — with
// AuthorizeInvocation(networkPassphrase, consumerSigner, contractID,
// "resolve_dispute", <the same args this call passes>, <a recent ledger>),
// and handed to the operator out of band (this is the "construct and sign
// the second party's authorization entry directly" CLAUDE.md §4 calls
// out — stellar-cli 28.0.0 cannot produce it for you).
//
// Verified live against a real dual-authorized resolve_dispute: this
// network's __check_auth accepts classic SorobanCredentialsTypeSorobanCredentialsAddress
// credentials for a plain G... consumer — not CAP-71 AddressV2. A first
// attempt using AddressV2 failed authentication; switching to Address
// (matching what simulateTransaction's own recorded template already
// used) succeeded. AuthorizeInvocation uses Address for this reason.
func (r *StatementRegistry) ResolveDispute(
	ctx context.Context,
	signer *keypair.Full,
	seq uint64,
	resolutionHash [32]byte,
	amountCredited *big.Int,
	consumerAuth xdr.SorobanAuthorizationEntry,
) (string, error) {
	args, err := ResolveDisputeArgs(signer.Address(), seq, resolutionHash, amountCredited)
	if err != nil {
		return "", fmt.Errorf("statement_registry: resolve_dispute: %w", err)
	}
	_, hash, err := InvokeAndSubmitWithAuth(ctx, r.Client, r.NetworkPassphrase, signer, r.ContractID, "resolve_dispute", args,
		[]xdr.SorobanAuthorizationEntry{consumerAuth})
	if err != nil {
		return hash, fmt.Errorf("statement_registry: resolve_dispute: %w", err)
	}
	return hash, nil
}

// ExtendStatementTTL extends seq's storage TTL by ledgers, authorized by
// signer as the operator.
func (r *StatementRegistry) ExtendStatementTTL(ctx context.Context, signer *keypair.Full, seq uint64, ledgers uint32) (string, error) {
	operatorArg, err := ScvAddress(signer.Address())
	if err != nil {
		return "", fmt.Errorf("statement_registry: extend_statement_ttl: %w", err)
	}
	args := []xdr.ScVal{operatorArg, ScvU64(seq), ScvU32(ledgers)}
	_, hash, err := InvokeAndSubmit(ctx, r.Client, r.NetworkPassphrase, signer, r.ContractID, "extend_statement_ttl", args)
	if err != nil {
		return hash, fmt.Errorf("statement_registry: extend_statement_ttl: %w", err)
	}
	return hash, nil
}

// GetStatement returns the statement anchored under operator at seq.
func (r *StatementRegistry) GetStatement(ctx context.Context, operator string, seq uint64) (*Statement, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_statement: %w", err)
	}
	result, err := SimulateCall(ctx, r.Client, readOnlySourceAccount, r.ContractID, "get_statement", []xdr.ScVal{operatorArg, ScvU64(seq)})
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_statement: %w", err)
	}
	stmt, err := decodeStatement(result)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_statement: %w", err)
	}
	return stmt, nil
}

// ListStatements returns every sequence number anchored under operator
// against consumer.
func (r *StatementRegistry) ListStatements(ctx context.Context, operator, consumer string) ([]uint64, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: list_statements: %w", err)
	}
	consumerArg, err := ScvAddress(consumer)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: list_statements: %w", err)
	}
	result, err := SimulateCall(ctx, r.Client, readOnlySourceAccount, r.ContractID, "list_statements", []xdr.ScVal{operatorArg, consumerArg})
	if err != nil {
		return nil, fmt.Errorf("statement_registry: list_statements: %w", err)
	}
	items, err := DecodeVec(result)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: list_statements: decode result: %w", err)
	}
	seqs := make([]uint64, len(items))
	for i, item := range items {
		seqs[i], err = DecodeU64(item)
		if err != nil {
			return nil, fmt.Errorf("statement_registry: list_statements: decode item %d: %w", i, err)
		}
	}
	return seqs, nil
}

// GetDispute returns seq's dispute.
func (r *StatementRegistry) GetDispute(ctx context.Context, operator string, seq uint64) (*Dispute, error) {
	operatorArg, err := ScvAddress(operator)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_dispute: %w", err)
	}
	result, err := SimulateCall(ctx, r.Client, readOnlySourceAccount, r.ContractID, "get_dispute", []xdr.ScVal{operatorArg, ScvU64(seq)})
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_dispute: %w", err)
	}
	dispute, err := decodeDispute(result)
	if err != nil {
		return nil, fmt.Errorf("statement_registry: get_dispute: %w", err)
	}
	return dispute, nil
}

// --- decoding helpers ---

// scvProtocol encodes a Protocol as its unit-enum wire form: Vec[Symbol].
func scvProtocol(p Protocol) xdr.ScVal {
	return ScvVec([]xdr.ScVal{ScvSymbol(string(p))})
}

// decodeUnitEnumVariant decodes a soroban_sdk unit-variant enum: a
// one-element Vec holding the variant's Symbol.
func decodeUnitEnumVariant(v xdr.ScVal) (string, error) {
	items, err := DecodeVec(v)
	if err != nil {
		return "", err
	}
	if len(items) != 1 {
		return "", fmt.Errorf("stellar: unit enum vec has %d items, want 1", len(items))
	}
	return DecodeSymbol(items[0])
}

func decodeProtocol(v xdr.ScVal) (Protocol, error) {
	s, err := decodeUnitEnumVariant(v)
	if err != nil {
		return "", fmt.Errorf("decode protocol: %w", err)
	}
	return Protocol(s), nil
}

func decodeStatus(v xdr.ScVal) (Status, error) {
	s, err := decodeUnitEnumVariant(v)
	if err != nil {
		return "", fmt.Errorf("decode status: %w", err)
	}
	return Status(s), nil
}

// scvOptionalAddress encodes *string as ScVal::Void when nil, or as an
// Address otherwise — the wire form of Rust's Option<Address>.
func scvOptionalAddress(addr *string) (xdr.ScVal, error) {
	if addr == nil {
		return ScvVoid(), nil
	}
	return ScvAddress(*addr)
}

// decodeOptionalAddress reverses scvOptionalAddress.
func decodeOptionalAddress(v xdr.ScVal) (*string, error) {
	if v.Type == xdr.ScValTypeScvVoid {
		return nil, nil
	}
	addr, err := DecodeAddress(v)
	if err != nil {
		return nil, err
	}
	return &addr, nil
}

func decodeBytes32(v xdr.ScVal) ([32]byte, error) {
	var out [32]byte
	b, err := DecodeBytes(v)
	if err != nil {
		return out, err
	}
	if len(b) != 32 {
		return out, fmt.Errorf("stellar: expected 32 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

func decodeStatement(v xdr.ScVal) (*Statement, error) {
	m, err := DecodeMap(v)
	if err != nil {
		return nil, fmt.Errorf("decode Statement: %w", err)
	}
	get := func(key string) (xdr.ScVal, error) { return MapField(m, key) }

	amountBilledVal, err := get("amount_billed")
	if err != nil {
		return nil, err
	}
	amountBilled, err := DecodeI128(amountBilledVal)
	if err != nil {
		return nil, fmt.Errorf("field amount_billed: %w", err)
	}

	amountSettledVal, err := get("amount_settled")
	if err != nil {
		return nil, err
	}
	amountSettled, err := DecodeI128(amountSettledVal)
	if err != nil {
		return nil, fmt.Errorf("field amount_settled: %w", err)
	}

	anchoredLedgerVal, err := get("anchored_ledger")
	if err != nil {
		return nil, err
	}
	anchoredLedger, err := DecodeU32(anchoredLedgerVal)
	if err != nil {
		return nil, fmt.Errorf("field anchored_ledger: %w", err)
	}

	channelVal, err := get("channel")
	if err != nil {
		return nil, err
	}
	channel, err := decodeOptionalAddress(channelVal)
	if err != nil {
		return nil, fmt.Errorf("field channel: %w", err)
	}

	consumerVal, err := get("consumer")
	if err != nil {
		return nil, err
	}
	consumer, err := DecodeAddress(consumerVal)
	if err != nil {
		return nil, fmt.Errorf("field consumer: %w", err)
	}

	operatorVal, err := get("operator")
	if err != nil {
		return nil, err
	}
	operator, err := DecodeAddress(operatorVal)
	if err != nil {
		return nil, fmt.Errorf("field operator: %w", err)
	}

	periodEndVal, err := get("period_end")
	if err != nil {
		return nil, err
	}
	periodEnd, err := DecodeU32(periodEndVal)
	if err != nil {
		return nil, fmt.Errorf("field period_end: %w", err)
	}

	periodStartVal, err := get("period_start")
	if err != nil {
		return nil, err
	}
	periodStart, err := DecodeU32(periodStartVal)
	if err != nil {
		return nil, fmt.Errorf("field period_start: %w", err)
	}

	priceBookVersionVal, err := get("price_book_version")
	if err != nil {
		return nil, err
	}
	priceBookVersion, err := DecodeU32(priceBookVersionVal)
	if err != nil {
		return nil, fmt.Errorf("field price_book_version: %w", err)
	}

	protocolVal, err := get("protocol")
	if err != nil {
		return nil, err
	}
	protocol, err := decodeProtocol(protocolVal)
	if err != nil {
		return nil, fmt.Errorf("field protocol: %w", err)
	}

	requestCountVal, err := get("request_count")
	if err != nil {
		return nil, err
	}
	requestCount, err := DecodeU64(requestCountVal)
	if err != nil {
		return nil, fmt.Errorf("field request_count: %w", err)
	}

	statusVal, err := get("status")
	if err != nil {
		return nil, err
	}
	status, err := decodeStatus(statusVal)
	if err != nil {
		return nil, fmt.Errorf("field status: %w", err)
	}

	tokenVal, err := get("token")
	if err != nil {
		return nil, err
	}
	token, err := DecodeAddress(tokenVal)
	if err != nil {
		return nil, fmt.Errorf("field token: %w", err)
	}

	usageRootVal, err := get("usage_root")
	if err != nil {
		return nil, err
	}
	usageRoot, err := decodeBytes32(usageRootVal)
	if err != nil {
		return nil, fmt.Errorf("field usage_root: %w", err)
	}

	return &Statement{
		Operator:         operator,
		Consumer:         consumer,
		PeriodStart:      periodStart,
		PeriodEnd:        periodEnd,
		UsageRoot:        usageRoot,
		RequestCount:     requestCount,
		Token:            token,
		AmountBilled:     amountBilled,
		AmountSettled:    amountSettled,
		PriceBookVersion: priceBookVersion,
		Protocol:         protocol,
		Channel:          channel,
		AnchoredLedger:   anchoredLedger,
		Status:           status,
	}, nil
}

func decodeDispute(v xdr.ScVal) (*Dispute, error) {
	m, err := DecodeMap(v)
	if err != nil {
		return nil, fmt.Errorf("decode Dispute: %w", err)
	}
	get := func(key string) (xdr.ScVal, error) { return MapField(m, key) }

	amountCreditedVal, err := get("amount_credited")
	if err != nil {
		return nil, err
	}
	amountCredited, err := DecodeI128(amountCreditedVal)
	if err != nil {
		return nil, fmt.Errorf("field amount_credited: %w", err)
	}

	consumerVal, err := get("consumer")
	if err != nil {
		return nil, err
	}
	consumer, err := DecodeAddress(consumerVal)
	if err != nil {
		return nil, fmt.Errorf("field consumer: %w", err)
	}

	openedLedgerVal, err := get("opened_ledger")
	if err != nil {
		return nil, err
	}
	openedLedger, err := DecodeU32(openedLedgerVal)
	if err != nil {
		return nil, fmt.Errorf("field opened_ledger: %w", err)
	}

	reasonHashVal, err := get("reason_hash")
	if err != nil {
		return nil, err
	}
	reasonHash, err := decodeBytes32(reasonHashVal)
	if err != nil {
		return nil, fmt.Errorf("field reason_hash: %w", err)
	}

	resolutionHashVal, err := get("resolution_hash")
	if err != nil {
		return nil, err
	}
	var resolutionHash *[32]byte
	if resolutionHashVal.Type != xdr.ScValTypeScvVoid {
		h, err := decodeBytes32(resolutionHashVal)
		if err != nil {
			return nil, fmt.Errorf("field resolution_hash: %w", err)
		}
		resolutionHash = &h
	}

	resolvedLedgerVal, err := get("resolved_ledger")
	if err != nil {
		return nil, err
	}
	var resolvedLedger *uint32
	if resolvedLedgerVal.Type != xdr.ScValTypeScvVoid {
		l, err := DecodeU32(resolvedLedgerVal)
		if err != nil {
			return nil, fmt.Errorf("field resolved_ledger: %w", err)
		}
		resolvedLedger = &l
	}

	return &Dispute{
		Consumer:       consumer,
		ReasonHash:     reasonHash,
		OpenedLedger:   openedLedger,
		ResolutionHash: resolutionHash,
		AmountCredited: amountCredited,
		ResolvedLedger: resolvedLedger,
	}, nil
}
