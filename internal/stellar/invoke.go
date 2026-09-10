package stellar

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// baseFee is the classic (non-resource) fee, in stroops, for a
// single-operation transaction. A real submission pays this plus the
// resource fee simulation reports.
const baseFee = 100

// pollInterval and pollTimeout bound how long InvokeAndSubmit waits for a
// submitted transaction to settle before giving up.
const (
	pollInterval = 2 * time.Second
	pollTimeout  = 30 * time.Second
)

// ErrSimulationFailed wraps simulateTransaction's own Error field — a
// simulation that ran but rejected the call, distinct from an RPCError
// (which means the request itself was malformed).
type ErrSimulationFailed struct{ Message string }

func (e *ErrSimulationFailed) Error() string {
	return fmt.Sprintf("stellar: simulation failed: %s", e.Message)
}

// ErrTransactionFailed indicates a transaction was accepted and settled,
// but with FAILED status rather than SUCCESS.
type ErrTransactionFailed struct {
	Hash      string
	ResultXDR string
}

func (e *ErrTransactionFailed) Error() string {
	return fmt.Sprintf("stellar: transaction %s failed on chain", e.Hash)
}

// SimulateCall builds a placeholder read-only transaction invoking
// function on the contract at contractID with args, and returns the
// decoded ScVal it returns. sourceAccount need not be funded or even
// exist — the RPC node's simulateTransaction never touches real account
// state for a call shaped like this, so callers may pass any well-formed
// G... address for a pure read (verified against a live testnet node; see
// pricebook_test.go).
func SimulateCall(ctx context.Context, client *Client, sourceAccount, contractID, function string, args []xdr.ScVal) (xdr.ScVal, error) {
	muxed, err := muxedAccount(sourceAccount)
	if err != nil {
		return xdr.ScVal{}, err
	}
	op, err := buildContractCallOperation(contractID, function, args, nil)
	if err != nil {
		return xdr.ScVal{}, err
	}

	tx := xdr.Transaction{
		SourceAccount: muxed,
		Fee:           baseFee,
		SeqNum:        1,
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:    []xdr.Operation{op},
		Ext:           xdr.TransactionExt{V: 0},
	}
	envelopeB64, err := marshalUnsignedEnvelope(tx)
	if err != nil {
		return xdr.ScVal{}, err
	}

	result, err := client.SimulateTransaction(ctx, SimulateTransactionParams{Transaction: envelopeB64})
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: simulate %s: %w", function, err)
	}
	if result.Error != "" {
		return xdr.ScVal{}, &ErrSimulationFailed{Message: result.Error}
	}
	if len(result.Results) == 0 {
		return xdr.ScVal{}, fmt.Errorf("stellar: simulate %s: no results", function)
	}
	return UnmarshalScValBase64(result.Results[0].XDR)
}

// InvokeAndSubmit performs a full write-path contract call authorized by
// signer: fetches signer's account sequence number, simulates to obtain
// the resource footprint, fee, and the authorization entries the call
// needs, signs, submits, and polls until the transaction settles. Returns
// the contract call's real on-chain return value — decoded from the
// settled transaction's own result metadata, not the value simulation
// predicted, since ledger state (and so the result) can differ between
// simulating and actually landing — and the transaction hash.
//
// This covers every write in §4 authorized by a single party who is also
// the transaction's source account (publish, anchor, open_dispute,
// extend_statement_ttl, and every one-way-channel mutator). resolve_dispute
// needs a second party's authorization too; see registry.go, which passes
// that in via invokeAndSubmit's extraAuth rather than through this
// exported entry point.
func InvokeAndSubmit(ctx context.Context, client *Client, networkPassphrase string, signer *keypair.Full, contractID, function string, args []xdr.ScVal) (xdr.ScVal, string, error) {
	return invokeAndSubmit(ctx, client, networkPassphrase, signer, contractID, function, args, nil)
}

func invokeAndSubmit(
	ctx context.Context,
	client *Client,
	networkPassphrase string,
	signer *keypair.Full,
	contractID, function string,
	args []xdr.ScVal,
	extraAuth []xdr.SorobanAuthorizationEntry,
) (xdr.ScVal, string, error) {
	source := signer.Address()

	seqNum, err := fetchSequenceNumber(ctx, client, source)
	if err != nil {
		return xdr.ScVal{}, "", err
	}

	muxed, err := muxedAccount(source)
	if err != nil {
		return xdr.ScVal{}, "", err
	}

	// First pass: simulate with no auth attached, to learn what
	// authorization and resources the call needs.
	unsignedOp, err := buildContractCallOperation(contractID, function, args, nil)
	if err != nil {
		return xdr.ScVal{}, "", err
	}
	simTx := xdr.Transaction{
		SourceAccount: muxed,
		Fee:           baseFee,
		SeqNum:        xdr.SequenceNumber(seqNum + 1),
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:    []xdr.Operation{unsignedOp},
		Ext:           xdr.TransactionExt{V: 0},
	}
	envelopeB64, err := marshalUnsignedEnvelope(simTx)
	if err != nil {
		return xdr.ScVal{}, "", err
	}

	simResult, err := client.SimulateTransaction(ctx, SimulateTransactionParams{Transaction: envelopeB64})
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: simulate %s: %w", function, err)
	}
	if simResult.Error != "" {
		return xdr.ScVal{}, "", &ErrSimulationFailed{Message: simResult.Error}
	}
	if len(simResult.Results) == 0 {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: simulate %s: no results", function)
	}

	recordedAuth, err := decodeAuthEntries(simResult.Results[0].Auth)
	if err != nil {
		return xdr.ScVal{}, "", err
	}
	allAuth := append(recordedAuth, extraAuth...)

	finalOp, err := buildContractCallOperation(contractID, function, args, allAuth)
	if err != nil {
		return xdr.ScVal{}, "", err
	}

	var sorobanData xdr.SorobanTransactionData
	sorobanDataBytes, err := base64.StdEncoding.DecodeString(simResult.TransactionData)
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: decode transaction data: %w", err)
	}
	if err := sorobanData.UnmarshalBinary(sorobanDataBytes); err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: unmarshal transaction data: %w", err)
	}

	var minResourceFee int64
	if simResult.MinResourceFee != "" {
		if _, err := fmt.Sscanf(simResult.MinResourceFee, "%d", &minResourceFee); err != nil {
			return xdr.ScVal{}, "", fmt.Errorf("stellar: parse minResourceFee %q: %w", simResult.MinResourceFee, err)
		}
	}

	finalTx := xdr.Transaction{
		SourceAccount: muxed,
		Fee:           xdr.Uint32(baseFee + minResourceFee),
		SeqNum:        xdr.SequenceNumber(seqNum + 1),
		Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
		Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
		Operations:    []xdr.Operation{finalOp},
		Ext:           xdr.TransactionExt{V: 1, SorobanData: &sorobanData},
	}

	hash, err := network.HashTransaction(finalTx, networkPassphrase)
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: hash transaction: %w", err)
	}
	sig, err := signer.SignDecorated(hash[:])
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: sign transaction: %w", err)
	}

	v1 := xdr.TransactionV1Envelope{Tx: finalTx, Signatures: []xdr.DecoratedSignature{sig}}
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &v1}
	envBytes, err := env.MarshalBinary()
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: marshal signed envelope: %w", err)
	}

	sendResult, err := client.SendTransaction(ctx, base64.StdEncoding.EncodeToString(envBytes))
	if err != nil {
		return xdr.ScVal{}, "", fmt.Errorf("stellar: submit %s: %w", function, err)
	}
	switch sendResult.Status {
	case SendTransactionStatusError:
		return xdr.ScVal{}, sendResult.Hash, fmt.Errorf("stellar: submit %s: rejected: %s", function, sendResult.ErrorResultXDR)
	case SendTransactionStatusPending, SendTransactionStatusDuplicate:
		// Accepted for inclusion; poll for the outcome below.
	default:
		return xdr.ScVal{}, sendResult.Hash, fmt.Errorf("stellar: submit %s: unexpected status %s", function, sendResult.Status)
	}

	txResult, err := pollTransaction(ctx, client, sendResult.Hash)
	if err != nil {
		return xdr.ScVal{}, sendResult.Hash, err
	}
	if txResult.Status != GetTransactionStatusSuccess {
		return xdr.ScVal{}, sendResult.Hash, &ErrTransactionFailed{Hash: sendResult.Hash, ResultXDR: txResult.ResultXDR}
	}

	returnVal, err := decodeReturnValue(txResult.ResultMetaXDR)
	if err != nil {
		return xdr.ScVal{}, sendResult.Hash, fmt.Errorf("stellar: %s succeeded but its return value could not be decoded: %w", function, err)
	}
	return returnVal, sendResult.Hash, nil
}

// --- shared helpers ---

func buildContractCallOperation(contractID, function string, args []xdr.ScVal, auth []xdr.SorobanAuthorizationEntry) (xdr.Operation, error) {
	contractAddr, err := scAddress(contractID)
	if err != nil {
		return xdr.Operation{}, err
	}
	invokeArgs := xdr.InvokeContractArgs{
		ContractAddress: contractAddr,
		FunctionName:    xdr.ScSymbol(function),
		Args:            args,
	}
	hf := xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract, InvokeContract: &invokeArgs}
	op := xdr.InvokeHostFunctionOp{HostFunction: hf, Auth: auth}
	return xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeInvokeHostFunction, InvokeHostFunctionOp: &op}}, nil
}

func muxedAccount(address string) (xdr.MuxedAccount, error) {
	accountID, err := accountIDFromAddress(address)
	if err != nil {
		return xdr.MuxedAccount{}, err
	}
	pk := xdr.PublicKey(accountID)
	return xdr.MuxedAccount{Type: xdr.CryptoKeyTypeKeyTypeEd25519, Ed25519: pk.Ed25519}, nil
}

func accountIDFromAddress(address string) (xdr.AccountId, error) {
	version, raw, err := strkey.DecodeAny(address)
	if err != nil {
		return xdr.AccountId{}, fmt.Errorf("stellar: decode strkey address %q: %w", address, err)
	}
	if version != strkey.VersionByteAccountID || len(raw) != 32 {
		return xdr.AccountId{}, fmt.Errorf("stellar: %q is not a valid G... account address", address)
	}
	var raw32 xdr.Uint256
	copy(raw32[:], raw)
	return xdr.AccountId(xdr.PublicKey{Type: xdr.PublicKeyTypePublicKeyTypeEd25519, Ed25519: &raw32}), nil
}

func marshalUnsignedEnvelope(tx xdr.Transaction) (string, error) {
	v1 := xdr.TransactionV1Envelope{Tx: tx}
	env := xdr.TransactionEnvelope{Type: xdr.EnvelopeTypeEnvelopeTypeTx, V1: &v1}
	b, err := env.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("stellar: marshal envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// fetchSequenceNumber returns account's current sequence number, read
// directly from its ledger entry.
func fetchSequenceNumber(ctx context.Context, client *Client, account string) (int64, error) {
	accountID, err := accountIDFromAddress(account)
	if err != nil {
		return 0, err
	}
	key := xdr.LedgerKey{Type: xdr.LedgerEntryTypeAccount, Account: &xdr.LedgerKeyAccount{AccountId: accountID}}
	keyBytes, err := key.MarshalBinary()
	if err != nil {
		return 0, fmt.Errorf("stellar: marshal account ledger key: %w", err)
	}

	result, err := client.GetLedgerEntries(ctx, []string{base64.StdEncoding.EncodeToString(keyBytes)})
	if err != nil {
		return 0, fmt.Errorf("stellar: fetch account %s: %w", account, err)
	}
	if len(result.Entries) == 0 {
		return 0, fmt.Errorf("stellar: account %s not found (never funded?)", account)
	}

	entryBytes, err := base64.StdEncoding.DecodeString(result.Entries[0].XDR)
	if err != nil {
		return 0, fmt.Errorf("stellar: decode account ledger entry: %w", err)
	}
	var entryData xdr.LedgerEntryData
	if err := entryData.UnmarshalBinary(entryBytes); err != nil {
		return 0, fmt.Errorf("stellar: unmarshal account ledger entry: %w", err)
	}
	if entryData.Account == nil {
		return 0, fmt.Errorf("stellar: ledger entry for %s is not an account entry", account)
	}
	return int64(entryData.Account.SeqNum), nil
}

func decodeAuthEntries(entries []string) ([]xdr.SorobanAuthorizationEntry, error) {
	out := make([]xdr.SorobanAuthorizationEntry, 0, len(entries))
	for _, b64 := range entries {
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return nil, fmt.Errorf("stellar: decode auth entry: %w", err)
		}
		var entry xdr.SorobanAuthorizationEntry
		if err := entry.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("stellar: unmarshal auth entry: %w", err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// decodeReturnValue extracts a settled transaction's real return value
// from its base64 TransactionMeta. Soroban's meta version has changed
// across protocol upgrades (V3 vs V4 carry the Soroban return value in
// different fields) — both are handled since either can be live depending
// on the network's current protocol version.
func decodeReturnValue(resultMetaXDR string) (xdr.ScVal, error) {
	raw, err := base64.StdEncoding.DecodeString(resultMetaXDR)
	if err != nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: decode result meta: %w", err)
	}
	var meta xdr.TransactionMeta
	if err := meta.UnmarshalBinary(raw); err != nil {
		return xdr.ScVal{}, fmt.Errorf("stellar: unmarshal result meta: %w", err)
	}
	switch {
	case meta.V3 != nil && meta.V3.SorobanMeta != nil:
		return meta.V3.SorobanMeta.ReturnValue, nil
	case meta.V4 != nil && meta.V4.SorobanMeta != nil && meta.V4.SorobanMeta.ReturnValue != nil:
		return *meta.V4.SorobanMeta.ReturnValue, nil
	default:
		return xdr.ScVal{}, fmt.Errorf("stellar: result meta (version %d) carries no Soroban return value", meta.V)
	}
}

func pollTransaction(ctx context.Context, client *Client, hash string) (*GetTransactionResult, error) {
	deadline := time.Now().Add(pollTimeout)
	for {
		result, err := client.GetTransaction(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("stellar: poll transaction %s: %w", hash, err)
		}
		if result.Status != GetTransactionStatusNotFound {
			return result, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("stellar: transaction %s did not settle within %s", hash, pollTimeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
