package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// commitmentsSchemaPool returns a testPool with every migration through
// 0003_commitments.sql applied.
func commitmentsSchemaPool(t *testing.T) (pool *pgxpool.Pool, ctx context.Context) {
	t.Helper()
	pool = testPool(t)
	ctx = context.Background()

	migrations, err := Migrations()
	if err != nil {
		t.Fatalf("Migrations returned unexpected error: %v", err)
	}
	if _, err := Migrate(ctx, pool, migrations); err != nil {
		t.Fatalf("Migrate returned unexpected error: %v", err)
	}
	return pool, ctx
}

const insertCommitmentTestSQL = `
INSERT INTO commitments (channel, cumulative_amount, signature, signer_key)
VALUES ($1, $2, $3, $4)`

func insertTestCommitment(ctx context.Context, pool *pgxpool.Pool, channel string, amount int64) error {
	_, err := pool.Exec(ctx, insertCommitmentTestSQL, channel, amount,
		[]byte("fake-signature-64-bytes-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX"),
		make([]byte, 32))
	return err
}

// TestCommitmentsMigration_MonotonicTrigger_RejectsRegression is the
// literal test the system prompt asks for: attempt the regression directly
// via SQL and assert it fails (§5).
func TestCommitmentsMigration_MonotonicTrigger_RejectsRegression(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	if err := insertTestCommitment(ctx, pool, "channel-a", 1000); err != nil {
		t.Fatalf("insert initial commitment: %v", err)
	}

	err := insertTestCommitment(ctx, pool, "channel-a", 500)
	if err == nil {
		t.Fatal("inserting a lower cumulative_amount than the channel's highest succeeded, want a rejection")
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error is not a *pgconn.PgError: %v", err)
	}
	if pgErr.Code != "TB001" {
		t.Errorf("error code = %q, want %q", pgErr.Code, "TB001")
	}

	// The regression must not have been written.
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM commitments WHERE channel = 'channel-a' AND cumulative_amount = 500`).
		Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("found %d rows for the rejected regression, want 0", count)
	}
}

func TestCommitmentsMigration_MonotonicTrigger_AllowsIncreasingAmounts(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	for _, amount := range []int64{100, 200, 300, 300_000_000} {
		if err := insertTestCommitment(ctx, pool, "channel-b", amount); err != nil {
			t.Fatalf("insert commitment at amount %d: %v", amount, err)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM commitments WHERE channel = 'channel-b'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 4 {
		t.Errorf("found %d rows, want 4", count)
	}
}

func TestCommitmentsMigration_MonotonicTrigger_AllowsFirstInsertAtAnyAmount(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	// No prior row for this channel exists, so current_max is NULL and any
	// positive amount must be accepted regardless of size.
	if err := insertTestCommitment(ctx, pool, "channel-c", 999_999_999); err != nil {
		t.Fatalf("first insert for a fresh channel returned unexpected error: %v", err)
	}
}

func TestCommitmentsMigration_MonotonicTrigger_ScopedPerChannel(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	if err := insertTestCommitment(ctx, pool, "channel-high", 1_000_000); err != nil {
		t.Fatalf("insert into channel-high: %v", err)
	}
	// A much lower amount for a completely different channel must not be
	// blocked by channel-high's unrelated history — the trigger scopes its
	// MAX() lookup to NEW.channel.
	if err := insertTestCommitment(ctx, pool, "channel-low", 1); err != nil {
		t.Fatalf("insert into channel-low returned unexpected error (trigger incorrectly scoped across channels?): %v", err)
	}
}

func TestCommitmentsMigration_MonotonicTrigger_EqualToCurrentMaxIsARegularUniqueViolation(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	if err := insertTestCommitment(ctx, pool, "channel-d", 500); err != nil {
		t.Fatalf("insert initial commitment: %v", err)
	}

	err := insertTestCommitment(ctx, pool, "channel-d", 500)
	if err == nil {
		t.Fatal("inserting a duplicate (channel, cumulative_amount) succeeded, want a unique violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error is not a *pgconn.PgError: %v", err)
	}
	// Postgres's own unique_violation code, not the custom TB001 the
	// monotonic trigger uses — an exact repeat is not a regression, it's a
	// duplicate, and the two must be distinguishable by error code alone.
	if pgErr.Code != "23505" {
		t.Errorf("error code = %q, want %q (unique_violation)", pgErr.Code, "23505")
	}
}

func TestCommitmentsMigration_CheckConstraints(t *testing.T) {
	pool, ctx := commitmentsSchemaPool(t)

	t.Run("cumulative_amount must be positive", func(t *testing.T) {
		if err := insertTestCommitment(ctx, pool, "channel-check-1", 0); err == nil {
			t.Error("inserted a zero cumulative_amount, want a check violation")
		}
		if err := insertTestCommitment(ctx, pool, "channel-check-1", -5); err == nil {
			t.Error("inserted a negative cumulative_amount, want a check violation")
		}
	})

	t.Run("signer_key must be 32 bytes", func(t *testing.T) {
		_, err := pool.Exec(ctx, insertCommitmentTestSQL, "channel-check-2", int64(1),
			[]byte("sig"), []byte("too-short"))
		if err == nil {
			t.Error("inserted a signer_key that is not 32 bytes, want a check violation")
		}
	})

	t.Run("valid requires verified_at", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO commitments (channel, cumulative_amount, signature, signer_key, valid) VALUES ($1, $2, $3, $4, $5)`,
			"channel-check-3", int64(1), []byte("sig"), make([]byte, 32), true)
		if err == nil {
			t.Error("inserted valid=true with verified_at still NULL, want a check violation")
		}
	})
}
