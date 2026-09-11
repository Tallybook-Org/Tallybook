// Package custody owns commitment storage and verification — the payment
// channel commitments a payer signs off-chain as they consume the API,
// which are the operator's only record of earned revenue until they are
// settled on chain (§1). "A lost commitment is lost revenue, and a missed
// deadline is lost revenue": every function in this package is written
// with that in mind.
package custody

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/big"
)

// Sentinel errors for VerifyCommitment. All are argument-validation
// failures — malformed input caught before ever asking the chain anything
// — distinct from a PrepareCommitment RPC error, which is wrapped without
// a sentinel since its underlying cause belongs to internal/stellar.
var (
	ErrNilAmount         = errors.New("custody: amount is nil")
	ErrNonPositiveAmount = errors.New("custody: amount must be positive")
	ErrInvalidSignerKey  = errors.New("custody: signer key is not a 32-byte ed25519 public key")
)

// ChannelReader is the one piece of a channel binding commitment
// verification needs: PrepareCommitment, the live simulate call that
// derives the exact bytes a funder's commitment_key must have signed for a
// given cumulative amount on this channel right now (§4: "Verify a
// commitment by simulating prepare_commitment before trusting it."). A
// narrow interface — satisfied by *internal/stellar.Channel — keeps this
// package testable without a live Soroban RPC endpoint.
type ChannelReader interface {
	PrepareCommitment(ctx context.Context, amount *big.Int) ([]byte, error)
}

// VerifyCommitment reports whether signature is a valid ed25519 signature,
// under trustedSignerKey, over the exact bytes prepare_commitment predicts
// on-chain right now for cumulative amount on channel.
//
// A non-nil error means verification could not be completed at all — a
// malformed argument, or the prepare_commitment simulation itself failing
// — which is different from a completed verification that simply found the
// signature invalid, reported as (false, nil). "Not valid" is a definite
// verification outcome, ready to be stored (§6 ordering rule 2: "store the
// verification outcome"); an error means there is no outcome yet, and a
// caller (Store.Submit, below) must not persist a commitment for it.
//
// trustedSignerKey is not read from the commitment being verified — it is
// supplied by the caller, sourced from wherever this channel's
// commitment_key was established when the channel was set up. §4 lists no
// on-chain getter for it: one-way-channel's own getters are limited to
// token, from, to, refund_waiting_period, deposited, balance, and
// withdrawn. This is a deliberate, opinionated reading of "verify a
// commitment" (§4): correctness of the signature bytes alone is not
// enough — a self-signed, internally-consistent commitment under an
// attacker's own key would pass that check trivially, and the caller
// choosing which key counts as authoritative for a channel is the only
// thing standing between "the funder signed this" and "someone signed
// this." Money is on the line, so the stricter reading wins — a
// commitment claiming to be signed by some other key is not verified
// against the wrong key, it is simply invalid.
func VerifyCommitment(ctx context.Context, channel ChannelReader, trustedSignerKey ed25519.PublicKey, amount *big.Int, signature []byte) (bool, error) {
	if amount == nil {
		return false, ErrNilAmount
	}
	if amount.Sign() <= 0 {
		return false, fmt.Errorf("%w: got %s", ErrNonPositiveAmount, amount)
	}
	if len(trustedSignerKey) != ed25519.PublicKeySize {
		return false, fmt.Errorf("%w: got %d bytes", ErrInvalidSignerKey, len(trustedSignerKey))
	}

	message, err := channel.PrepareCommitment(ctx, amount)
	if err != nil {
		return false, fmt.Errorf("custody: verify commitment: prepare_commitment: %w", err)
	}

	// ed25519.Verify itself handles a malformed (wrong-length) signature by
	// returning false, not panicking or erroring — exactly the "not valid"
	// outcome this function reports the same way for any other signature
	// mismatch.
	return ed25519.Verify(trustedSignerKey, message, signature), nil
}
