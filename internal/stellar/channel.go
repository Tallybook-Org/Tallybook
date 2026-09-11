package stellar

import (
	"context"
	"fmt"
	"math/big"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/network"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// Channel is a typed binding to a deployed one-way-channel contract
// instance (external, unaudited — stellar-experimental/one-way-channel;
// §4). Consumed, not forked or vendored.
//
// Every function signature and the commitment wire format below were
// verified directly against the contract's real source
// (github.com/stellar-experimental/one-way-channel, commit at the time of
// writing) rather than assumed — §4 documents this contract only at the
// level of function and getter names, deferring detail to that repo.
//
// Live invocation was not verified end to end the way price_book's and
// statement_registry's bindings were at the time this file was first
// written: deploying a fresh instance on testnet with `stellar contract
// deploy` trapped with "UnreachableCodeReached" in __constructor, which
// at the time looked like a dead end (it reproduced even with a
// completely empty constructor body). It wasn't one. Root-caused since:
// stellar-cli 28.0.0 mis-encodes a BytesN<32> CLI argument — this
// contract's commitment_key in particular — when it's given as a G...
// strkey address rather than hex, tripping the trap the instant the
// constructor touches the value, before any of the contract's own logic
// runs. The contract itself is fine; deploying it with commitment_key as
// hex works and was confirmed live (deploy, then real token/from/
// deposited/balance/refund_waiting_period reads all correct). See
// docs/operator-runbook.md for the working deploy command and full
// detail. This package's bindings don't touch deployment at all — they
// only invoke an already-deployed instance — so nothing here changes as
// a result; this note exists so the trap isn't mistaken for a live
// contract defect by anyone reading this file.
//
// What's independently verified here regardless: the commitment
// encoding, against the exact ScVal::Map format the contract's own doc
// comments specify (independently derived and checked, the same way
// internal/merkle's leaf encoding was — see channel_test.go), and event
// decoding, against the contract's real #[contractevent] struct
// definitions (see events.go).
type Channel struct {
	Client            *Client
	ContractID        string
	NetworkPassphrase string
}

// NewChannel returns a binding to the one-way-channel contract instance at
// contractID.
func NewChannel(client *Client, contractID, networkPassphrase string) *Channel {
	return &Channel{Client: client, ContractID: contractID, NetworkPassphrase: networkPassphrase}
}

// --- static getters (no auth) ---

// Token returns the channel's SEP-41 token address.
func (c *Channel) Token(ctx context.Context) (string, error) {
	return c.readAddress(ctx, "token")
}

// From returns the funder's address.
func (c *Channel) From(ctx context.Context) (string, error) {
	return c.readAddress(ctx, "from")
}

// To returns the recipient's address.
func (c *Channel) To(ctx context.Context) (string, error) {
	return c.readAddress(ctx, "to")
}

// RefundWaitingPeriod returns the number of ledgers the funder must wait
// after close_start before calling refund.
func (c *Channel) RefundWaitingPeriod(ctx context.Context) (uint32, error) {
	result, err := SimulateCall(ctx, c.Client, readOnlySourceAccount, c.ContractID, "refund_waiting_period", nil)
	if err != nil {
		return 0, fmt.Errorf("channel: refund_waiting_period: %w", err)
	}
	v, err := DecodeU32(result)
	if err != nil {
		return 0, fmt.Errorf("channel: refund_waiting_period: decode result: %w", err)
	}
	return v, nil
}

// --- dynamic getters (no auth) ---

// Deposited returns the total amount ever deposited into the channel.
func (c *Channel) Deposited(ctx context.Context) (*big.Int, error) {
	return c.readI128(ctx, "deposited")
}

// Balance returns the channel's current token balance.
func (c *Channel) Balance(ctx context.Context) (*big.Int, error) {
	return c.readI128(ctx, "balance")
}

// Withdrawn returns the total amount already withdrawn by the recipient.
func (c *Channel) Withdrawn(ctx context.Context) (*big.Int, error) {
	return c.readI128(ctx, "withdrawn")
}

// --- helpers (no auth) ---

// PrepareCommitment returns the exact bytes the funder must sign
// (ed25519, with the channel's commitment_key) to authorize the recipient
// to settle or close for cumulative amount. Simulating this before
// trusting a commitment the funder claims to have signed is how custody
// verifies one (§6) — never settle against a commitment that wasn't
// verified this way.
func (c *Channel) PrepareCommitment(ctx context.Context, amount *big.Int) ([]byte, error) {
	amountArg, err := ScvI128(amount)
	if err != nil {
		return nil, fmt.Errorf("channel: prepare_commitment: %w", err)
	}
	result, err := SimulateCall(ctx, c.Client, readOnlySourceAccount, c.ContractID, "prepare_commitment", []xdr.ScVal{amountArg})
	if err != nil {
		return nil, fmt.Errorf("channel: prepare_commitment: %w", err)
	}
	b, err := DecodeBytes(result)
	if err != nil {
		return nil, fmt.Errorf("channel: prepare_commitment: decode result: %w", err)
	}
	return b, nil
}

// BuildCommitment computes the same bytes PrepareCommitment would return,
// locally and without a network round trip: the contract's documented
// wire format is ScVal::Map{amount: I128, channel: Address(contractID),
// domain: Symbol("chancmmt"), network: BytesN<32>(sha256(passphrase))},
// XDR-encoded, sorted alphabetically by key (amount, channel, domain,
// network — already alphabetical). Useful for a funder signing many
// commitments without simulating each one; PrepareCommitment remains the
// source of truth for verifying a commitment someone else claims to have
// signed.
func BuildCommitment(contractID, networkPassphrase string, amount *big.Int) ([]byte, error) {
	amountVal, err := ScvI128(amount)
	if err != nil {
		return nil, fmt.Errorf("channel: build commitment: %w", err)
	}
	channelVal, err := ScvAddress(contractID)
	if err != nil {
		return nil, fmt.Errorf("channel: build commitment: %w", err)
	}
	networkID := network.ID(networkPassphrase)

	entries := []xdr.ScMapEntry{
		MapEntry("amount", amountVal),
		MapEntry("channel", channelVal),
		MapEntry("domain", ScvSymbol("chancmmt")),
		MapEntry("network", ScvBytes(networkID[:])),
	}
	b, err := ScvMap(entries).MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("channel: build commitment: marshal: %w", err)
	}
	return b, nil
}

// --- mutators ---

// TopUp deposits additional amount into the channel, authorized by signer
// as the funder.
func (c *Channel) TopUp(ctx context.Context, signer *keypair.Full, amount *big.Int) (string, error) {
	amountArg, err := ScvI128(amount)
	if err != nil {
		return "", fmt.Errorf("channel: top_up: %w", err)
	}
	_, hash, err := InvokeAndSubmit(ctx, c.Client, c.NetworkPassphrase, signer, c.ContractID, "top_up", []xdr.ScVal{amountArg})
	if err != nil {
		return hash, fmt.Errorf("channel: top_up: %w", err)
	}
	return hash, nil
}

// Settle withdraws the difference between amount and what's already been
// withdrawn to the recipient, without closing the channel, authorized by
// signer as the recipient. sig is the funder's ed25519 signature (via the
// commitment_key, not a Stellar account) over PrepareCommitment(amount).
func (c *Channel) Settle(ctx context.Context, signer *keypair.Full, amount *big.Int, sig [64]byte) (string, error) {
	args, err := settleOrCloseArgs(amount, sig)
	if err != nil {
		return "", fmt.Errorf("channel: settle: %w", err)
	}
	_, hash, err := InvokeAndSubmit(ctx, c.Client, c.NetworkPassphrase, signer, c.ContractID, "settle", args)
	if err != nil {
		return hash, fmt.Errorf("channel: settle: %w", err)
	}
	return hash, nil
}

// Close closes the channel using a signed commitment, withdrawing the
// unsettled difference to the recipient and attempting to refund the
// funder, authorized by signer as the recipient. sig is as in Settle.
func (c *Channel) Close(ctx context.Context, signer *keypair.Full, amount *big.Int, sig [64]byte) (string, error) {
	args, err := settleOrCloseArgs(amount, sig)
	if err != nil {
		return "", fmt.Errorf("channel: close: %w", err)
	}
	_, hash, err := InvokeAndSubmit(ctx, c.Client, c.NetworkPassphrase, signer, c.ContractID, "close", args)
	if err != nil {
		return hash, fmt.Errorf("channel: close: %w", err)
	}
	return hash, nil
}

// CloseStart begins closing the channel, effective after
// RefundWaitingPeriod ledgers, authorized by signer as the funder. This is
// the event the settler must react to immediately (§6): once observed,
// every sweep threshold is overridden and settlement happens now.
func (c *Channel) CloseStart(ctx context.Context, signer *keypair.Full) (string, error) {
	_, hash, err := InvokeAndSubmit(ctx, c.Client, c.NetworkPassphrase, signer, c.ContractID, "close_start", nil)
	if err != nil {
		return hash, fmt.Errorf("channel: close_start: %w", err)
	}
	return hash, nil
}

// Refund returns the channel's entire remaining balance to the funder,
// authorized by signer as the funder, once the close is effective. This
// is the transfer that makes every unsettled commitment worthless — the
// contract reserves nothing for the recipient (§1).
func (c *Channel) Refund(ctx context.Context, signer *keypair.Full) (string, error) {
	_, hash, err := InvokeAndSubmit(ctx, c.Client, c.NetworkPassphrase, signer, c.ContractID, "refund", nil)
	if err != nil {
		return hash, fmt.Errorf("channel: refund: %w", err)
	}
	return hash, nil
}

// --- shared helpers ---

func (c *Channel) readAddress(ctx context.Context, function string) (string, error) {
	result, err := SimulateCall(ctx, c.Client, readOnlySourceAccount, c.ContractID, function, nil)
	if err != nil {
		return "", fmt.Errorf("channel: %s: %w", function, err)
	}
	addr, err := DecodeAddress(result)
	if err != nil {
		return "", fmt.Errorf("channel: %s: decode result: %w", function, err)
	}
	return addr, nil
}

func (c *Channel) readI128(ctx context.Context, function string) (*big.Int, error) {
	result, err := SimulateCall(ctx, c.Client, readOnlySourceAccount, c.ContractID, function, nil)
	if err != nil {
		return nil, fmt.Errorf("channel: %s: %w", function, err)
	}
	v, err := DecodeI128(result)
	if err != nil {
		return nil, fmt.Errorf("channel: %s: decode result: %w", function, err)
	}
	return v, nil
}

func settleOrCloseArgs(amount *big.Int, sig [64]byte) ([]xdr.ScVal, error) {
	amountArg, err := ScvI128(amount)
	if err != nil {
		return nil, err
	}
	return []xdr.ScVal{amountArg, ScvBytes(sig[:])}, nil
}
