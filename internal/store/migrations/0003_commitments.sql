-- commitments: custody. THE MOST IMPORTANT TABLE IN THIS SCHEMA — see §6.
-- Columns match §5 exactly, plus one addition: `valid`. §6's ordering rule 2
-- ("Verify before storing... store the verification outcome") and its
-- failure-handling section ("A commitment that fails verification: store
-- it, mark it invalid...") both require representing a definite pass/fail
-- verdict, not just whether verification was attempted. verified_at alone
-- can't carry that: verification always happens synchronously, before a row
-- is ever written (see internal/custody.Store.Submit), so verified_at being
-- set is guaranteed the instant a row exists at all — it cannot also do
-- double duty distinguishing "verified and accepted" from "verified and
-- rejected". `valid` carries that verdict explicitly instead.
CREATE TABLE commitments (
    id                BIGSERIAL PRIMARY KEY,
    channel           TEXT NOT NULL,
    cumulative_amount NUMERIC(39, 0) NOT NULL,
    signature         BYTEA NOT NULL,
    signer_key        BYTEA NOT NULL,
    received_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    verified_at       TIMESTAMPTZ,
    valid             BOOLEAN,

    CONSTRAINT commitments_cumulative_amount_positive CHECK (cumulative_amount > 0),
    CONSTRAINT commitments_signer_key_length CHECK (octet_length(signer_key) = 32),
    CONSTRAINT commitments_valid_requires_verified_at CHECK (valid IS NULL OR verified_at IS NOT NULL),

    UNIQUE (channel, cumulative_amount)
);
CREATE INDEX ON commitments (channel, cumulative_amount DESC);

-- The monotonic invariant, enforced here rather than trusted to application
-- code (system prompt §5): a commitment whose cumulative_amount is lower
-- than the highest already stored for its channel must never be written.
-- An exact repeat of the current highest is a different case, deliberately
-- not raised by this trigger — it's caught by the UNIQUE constraint above
-- instead, and treated as an idempotent retry by internal/custody, not a
-- monotonic violation.
--
-- ERRCODE 'TB001' is a custom SQLSTATE (Postgres reserves none of the
-- standard codes for this), chosen so callers — internal/custody in
-- particular — can distinguish this specific invariant violation from any
-- other error via pgconn.PgError.Code, rather than pattern-matching the
-- exception message.
CREATE OR REPLACE FUNCTION commitments_reject_regression() RETURNS TRIGGER AS $$
DECLARE
    current_max NUMERIC(39, 0);
BEGIN
    SELECT MAX(cumulative_amount) INTO current_max
    FROM commitments
    WHERE channel = NEW.channel;

    IF current_max IS NOT NULL AND NEW.cumulative_amount < current_max THEN
        RAISE EXCEPTION 'commitments: cumulative_amount % for channel % is lower than the highest already stored (%)',
            NEW.cumulative_amount, NEW.channel, current_max
            USING ERRCODE = 'TB001';
    END IF;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER commitments_monotonic_before_insert
    BEFORE INSERT ON commitments
    FOR EACH ROW
    EXECUTE FUNCTION commitments_reject_regression();
