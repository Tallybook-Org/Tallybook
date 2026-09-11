package custody

import (
	"context"
	"crypto/ed25519"
	"errors"
	"math/big"
	"testing"
)

type fakeChannelReader struct {
	// message, if set, is returned verbatim regardless of amount.
	message []byte
	// messageFor, if set, takes priority over message and lets a test vary
	// the returned bytes by amount (e.g. to prove a signature for one
	// amount does not verify against another).
	messageFor func(amount *big.Int) []byte
	err        error
}

func (f fakeChannelReader) PrepareCommitment(_ context.Context, amount *big.Int) ([]byte, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.messageFor != nil {
		return f.messageFor(amount), nil
	}
	return f.message, nil
}

func TestVerifyCommitment_ValidSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("the exact bytes prepare_commitment would predict")
	sig := ed25519.Sign(priv, message)

	channel := fakeChannelReader{message: message}
	valid, err := VerifyCommitment(context.Background(), channel, pub, big.NewInt(100), sig)
	if err != nil {
		t.Fatalf("VerifyCommitment returned unexpected error: %v", err)
	}
	if !valid {
		t.Error("VerifyCommitment = false for a genuinely valid signature")
	}
}

func TestVerifyCommitment_WrongKeyIsInvalidNotError(t *testing.T) {
	_, funderPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	attackerPub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("the exact bytes prepare_commitment would predict")
	sig := ed25519.Sign(funderPriv, message) // signed by the funder...

	channel := fakeChannelReader{message: message}
	// ...but verified against the attacker's own key, not the funder's —
	// this is exactly the "self-signed, internally-consistent commitment
	// under an attacker's own key" scenario the doc comment calls out.
	valid, err := VerifyCommitment(context.Background(), channel, attackerPub, big.NewInt(100), sig)
	if err != nil {
		t.Fatalf("VerifyCommitment returned unexpected error: %v", err)
	}
	if valid {
		t.Error("VerifyCommitment = true for a signature under the wrong key")
	}
}

func TestVerifyCommitment_SignatureForADifferentAmountIsInvalid(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	channel := fakeChannelReader{messageFor: func(amount *big.Int) []byte {
		return []byte("commitment for amount " + amount.String())
	}}

	sigFor100 := ed25519.Sign(priv, []byte("commitment for amount 100"))

	// The same signature, valid for amount 100, must not verify for a
	// different amount's (different) message.
	valid, err := VerifyCommitment(context.Background(), channel, pub, big.NewInt(200), sigFor100)
	if err != nil {
		t.Fatalf("VerifyCommitment returned unexpected error: %v", err)
	}
	if valid {
		t.Error("VerifyCommitment = true for a signature over a different amount")
	}
}

func TestVerifyCommitment_MalformedSignatureIsInvalidNotError(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	channel := fakeChannelReader{message: []byte("message")}

	valid, err := VerifyCommitment(context.Background(), channel, pub, big.NewInt(1), []byte("too-short"))
	if err != nil {
		t.Fatalf("VerifyCommitment returned unexpected error: %v", err)
	}
	if valid {
		t.Error("VerifyCommitment = true for a malformed (wrong-length) signature")
	}
}

func TestVerifyCommitment_NilAmount(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	_, err := VerifyCommitment(context.Background(), fakeChannelReader{}, pub, nil, []byte("sig"))
	if !errors.Is(err, ErrNilAmount) {
		t.Fatalf("error = %v, want ErrNilAmount", err)
	}
}

func TestVerifyCommitment_NonPositiveAmount(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	for _, amount := range []int64{0, -1, -1000} {
		_, err := VerifyCommitment(context.Background(), fakeChannelReader{}, pub, big.NewInt(amount), []byte("sig"))
		if !errors.Is(err, ErrNonPositiveAmount) {
			t.Errorf("amount %d: error = %v, want ErrNonPositiveAmount", amount, err)
		}
	}
}

func TestVerifyCommitment_InvalidSignerKeyLength(t *testing.T) {
	_, err := VerifyCommitment(context.Background(), fakeChannelReader{}, []byte("too-short"), big.NewInt(1), []byte("sig"))
	if !errors.Is(err, ErrInvalidSignerKey) {
		t.Fatalf("error = %v, want ErrInvalidSignerKey", err)
	}
}

func TestVerifyCommitment_PrepareCommitmentErrorPropagates(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	boom := errors.New("rpc: simulateTransaction failed")
	channel := fakeChannelReader{err: boom}

	_, err := VerifyCommitment(context.Background(), channel, pub, big.NewInt(1), []byte("sig"))
	if !errors.Is(err, boom) {
		t.Fatalf("error = %v, want it to wrap %v", err, boom)
	}
}
