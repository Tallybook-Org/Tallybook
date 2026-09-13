package custody

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Tallybook-Org/tallybook/internal/store"
)

// testStorePool connects to the local docker-compose Postgres with the
// real migrations applied, in a fresh database scoped to this test alone —
// the same pattern internal/store's own tests use, duplicated here rather
// than exported from that package, since a test helper crossing a package
// boundary just to be shared is worse than one screenful of setup code
// each package owns.
func testStorePool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, "postgres://tallybook:tallybook@localhost:5433/tallybook?sslmode=disable")
	if err != nil {
		t.Skipf("skipping: cannot reach local postgres (is `docker compose up -d` running?): %v", err)
	}
	defer adminPool.Close()
	if err := adminPool.Ping(ctx); err != nil {
		t.Skipf("skipping: cannot reach local postgres (is `docker compose up -d` running?): %v", err)
	}

	dbName := fmt.Sprintf("tb_test_custody_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create test database %s: %v", dbName, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cleanupPool, err := pgxpool.New(cleanupCtx, "postgres://tallybook:tallybook@localhost:5433/tallybook?sslmode=disable")
		if err != nil {
			return
		}
		defer cleanupPool.Close()
		_, _ = cleanupPool.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+dbName)
	})

	pool, err := pgxpool.New(ctx, "postgres://tallybook:tallybook@localhost:5433/"+dbName+"?sslmode=disable")
	if err != nil {
		t.Fatalf("connect to test database %s: %v", dbName, err)
	}
	t.Cleanup(pool.Close)

	migrations, err := store.Migrations()
	if err != nil {
		t.Fatalf("store.Migrations returned unexpected error: %v", err)
	}
	if _, err := store.Migrate(context.Background(), pool, migrations); err != nil {
		t.Fatalf("store.Migrate returned unexpected error: %v", err)
	}
	return pool
}

func rowCount(t *testing.T, pool *pgxpool.Pool, channel string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM commitments WHERE channel = $1`, channel).Scan(&count); err != nil {
		t.Fatalf("count rows for channel %s: %v", channel, err)
	}
	return count
}

// TestStore_Submit_PersistsBeforeReturning is ordering rule 1 made
// concrete: by the time Submit returns successfully, the row must already
// be visible to a completely separate connection — proof it is committed,
// not merely buffered — because a caller is only supposed to acknowledge
// the payer after Submit returns, and that acknowledgement is worthless if
// the row isn't durable yet.
func TestStore_Submit_PersistsBeforeReturning(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("commitment message")
	sig := ed25519.Sign(priv, message)
	channel := fakeChannelReader{message: message}

	rec, err := s.Submit(context.Background(), channel, SubmittedCommitment{
		Channel: "channel-persist", CumulativeAmount: big.NewInt(1000), Signature: sig, TrustedSignerKey: pub,
	})
	if err != nil {
		t.Fatalf("Submit returned unexpected error: %v", err)
	}
	if !rec.Valid {
		t.Fatal("Submit recorded a valid signature as invalid")
	}
	if rec.VerifiedAt.IsZero() {
		t.Error("Record.VerifiedAt is zero, want it set")
	}

	// A brand new pool/connection, not the one Submit used, must already
	// see the row.
	freshPool, err := pgxpool.New(context.Background(), pool.Config().ConnString())
	if err != nil {
		t.Fatalf("open fresh connection: %v", err)
	}
	defer freshPool.Close()

	var count int
	if err := freshPool.QueryRow(context.Background(),
		`SELECT count(*) FROM commitments WHERE id = $1`, rec.ID).Scan(&count); err != nil {
		t.Fatalf("query via fresh connection: %v", err)
	}
	if count != 1 {
		t.Errorf("fresh connection sees %d rows for id %d, want 1 (row not durably committed?)", count, rec.ID)
	}
}

// TestStore_Submit_RetryAfterCrashIsIdempotent simulates the exact crash
// window ordering rule 1 exists for: Submit succeeded and persisted the
// row, but the collector crashed (or the response was lost) before the
// payer ever saw the acknowledgement, so the payer's client retries the
// identical commitment.
func TestStore_Submit_RetryAfterCrashIsIdempotent(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("commitment message")
	sig := ed25519.Sign(priv, message)
	channel := fakeChannelReader{message: message}
	sc := SubmittedCommitment{Channel: "channel-retry", CumulativeAmount: big.NewInt(500), Signature: sig, TrustedSignerKey: pub}

	first, err := s.Submit(context.Background(), channel, sc)
	if err != nil {
		t.Fatalf("first Submit returned unexpected error: %v", err)
	}
	if first.AlreadyRecorded {
		t.Error("first Submit reported AlreadyRecorded=true, want false")
	}

	second, err := s.Submit(context.Background(), channel, sc)
	if err != nil {
		t.Fatalf("second (retried) Submit returned unexpected error: %v", err)
	}
	if !second.AlreadyRecorded {
		t.Error("second Submit reported AlreadyRecorded=false, want true (it's a retry)")
	}
	if second.ID != first.ID {
		t.Errorf("second Submit's ID = %d, want %d (same row as the first)", second.ID, first.ID)
	}
	if second.Valid != first.Valid {
		t.Errorf("second Submit's Valid = %v, want %v (same as the first)", second.Valid, first.Valid)
	}

	if got := rowCount(t, pool, "channel-retry"); got != 1 {
		t.Errorf("channel-retry has %d rows after a retry, want 1 (no duplicate written)", got)
	}
}

// TestStore_Submit_NeverPersistsOnVerificationRPCFailure is ordering rule
// 2 made concrete: if prepare_commitment itself cannot be simulated (an
// RPC outage, say), there is no verification outcome yet, so nothing may
// be written — a caller must retry Submit itself once the RPC recovers,
// not be left with an ambiguous half-recorded commitment.
func TestStore_Submit_NeverPersistsOnVerificationRPCFailure(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	boom := errors.New("rpc: simulateTransaction: connection refused")
	channel := fakeChannelReader{err: boom}

	_, err = s.Submit(context.Background(), channel, SubmittedCommitment{
		Channel: "channel-rpc-down", CumulativeAmount: big.NewInt(100), Signature: []byte("sig"), TrustedSignerKey: pub,
	})
	if !errors.Is(err, ErrVerificationIncomplete) {
		t.Fatalf("Submit error = %v, want it to wrap ErrVerificationIncomplete", err)
	}

	if got := rowCount(t, pool, "channel-rpc-down"); got != 0 {
		t.Errorf("channel-rpc-down has %d rows after a failed verification attempt, want 0", got)
	}
}

// TestStore_Submit_StoresInvalidCommitmentsToo is the failure-handling
// requirement made concrete: a commitment that fails verification is not
// silently discarded, it is stored with valid=false — evidence for an
// alert, never a candidate for settlement.
func TestStore_Submit_StoresInvalidCommitmentsToo(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	_, wrongPriv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	trustedPub, _, err := ed25519.GenerateKey(nil) // a different key than the one that actually signed
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("commitment message")
	sig := ed25519.Sign(wrongPriv, message)
	channel := fakeChannelReader{message: message}

	rec, err := s.Submit(context.Background(), channel, SubmittedCommitment{
		Channel: "channel-invalid", CumulativeAmount: big.NewInt(700), Signature: sig, TrustedSignerKey: trustedPub,
	})
	if err != nil {
		t.Fatalf("Submit returned unexpected error for a completed-but-invalid verification: %v", err)
	}
	if rec.Valid {
		t.Error("Record.Valid = true for a signature that should not have verified")
	}
	if rec.VerifiedAt.IsZero() {
		t.Error("Record.VerifiedAt is zero even though verification completed")
	}
	if got := rowCount(t, pool, "channel-invalid"); got != 1 {
		t.Errorf("channel-invalid has %d rows, want 1 (invalid commitments must still be stored)", got)
	}
}

// TestStore_Submit_RejectsMonotonicRegression confirms the database's
// safety net (§5's BEFORE INSERT trigger) is reachable — and effective —
// through Store, not only via raw SQL: a regression is neither silently
// accepted nor silently dropped, it comes back as a distinguishable error,
// and nothing is written for it.
func TestStore_Submit_RejectsMonotonicRegression(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	channel := fakeChannelReader{messageFor: func(amount *big.Int) []byte {
		return []byte("commitment for amount " + amount.String())
	}}

	sign := func(amount int64) []byte {
		return ed25519.Sign(priv, []byte(fmt.Sprintf("commitment for amount %d", amount)))
	}

	if _, err := s.Submit(context.Background(), channel, SubmittedCommitment{
		Channel: "channel-monotonic", CumulativeAmount: big.NewInt(1000), Signature: sign(1000), TrustedSignerKey: pub,
	}); err != nil {
		t.Fatalf("initial Submit returned unexpected error: %v", err)
	}

	_, err = s.Submit(context.Background(), channel, SubmittedCommitment{
		Channel: "channel-monotonic", CumulativeAmount: big.NewInt(500), Signature: sign(500), TrustedSignerKey: pub,
	})
	if !errors.Is(err, ErrMonotonicViolation) {
		t.Fatalf("regression Submit error = %v, want it to wrap ErrMonotonicViolation", err)
	}

	if got := rowCount(t, pool, "channel-monotonic"); got != 1 {
		t.Errorf("channel-monotonic has %d rows, want 1 (the regression must not have been written)", got)
	}
}

// TestStore_Submit_ExactRepeatIsIdempotentNotMonotonicViolation is the
// boundary the trigger's strict "<" (not "<=") is there to draw: an exact
// repeat of the current highest cumulative_amount is not a regression, it
// is the same commitment arriving twice — the shape a payer's client retry
// takes. The trigger must let it through so the table's own UNIQUE
// constraint, not TB001, is what turns the second insert into
// ON CONFLICT DO NOTHING, and Submit must in turn surface that as a
// successful, idempotent read-back rather than any error — ErrMonotonicViolation
// least of all.
func TestStore_Submit_ExactRepeatIsIdempotentNotMonotonicViolation(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	message := []byte("commitment message")
	sig := ed25519.Sign(priv, message)
	channel := fakeChannelReader{message: message}
	sc := SubmittedCommitment{Channel: "channel-exact-repeat", CumulativeAmount: big.NewInt(1000), Signature: sig, TrustedSignerKey: pub}

	first, err := s.Submit(context.Background(), channel, sc)
	if err != nil {
		t.Fatalf("first Submit returned unexpected error: %v", err)
	}

	// Same channel, same amount, same signature — submitted again, exactly
	// as a client retrying the identical commitment would.
	second, err := s.Submit(context.Background(), channel, sc)
	if errors.Is(err, ErrMonotonicViolation) {
		t.Fatalf("second Submit of an exact repeat surfaced ErrMonotonicViolation: %v — the trigger's condition "+
			"must be a strict less-than, not <=, so an exact repeat of the current max falls through to the "+
			"UNIQUE constraint instead of raising TB001", err)
	}
	if err != nil {
		t.Fatalf("second Submit of an exact repeat returned unexpected error: %v", err)
	}
	if !second.AlreadyRecorded {
		t.Error("second Submit of an exact repeat reported AlreadyRecorded=false, want true")
	}
	if second.ID != first.ID {
		t.Errorf("second Submit's ID = %d, want %d (same row as the first)", second.ID, first.ID)
	}

	if got := rowCount(t, pool, "channel-exact-repeat"); got != 1 {
		t.Errorf("channel-exact-repeat has %d rows, want 1 (no duplicate written)", got)
	}
}

func TestStore_Submit_NilAmount(t *testing.T) {
	pool := testStorePool(t)
	s := NewStore(pool)
	pub, _, _ := ed25519.GenerateKey(nil)

	_, err := s.Submit(context.Background(), fakeChannelReader{}, SubmittedCommitment{
		Channel: "channel-nil-amount", CumulativeAmount: nil, Signature: []byte("sig"), TrustedSignerKey: pub,
	})
	if !errors.Is(err, ErrNilAmount) {
		t.Fatalf("error = %v, want ErrNilAmount", err)
	}
}
