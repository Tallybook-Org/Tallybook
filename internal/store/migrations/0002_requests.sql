-- requests: one row per metered request. The hot path. Columns and types
-- match §5 of the system prompt exactly; the CHECK constraints are an
-- addition beyond the literal spec, enforcing invariants the collector
-- must already honour (32-byte identifiers, channel set iff mpp_session)
-- at the database level rather than trusting application code alone.
CREATE TABLE requests (
    id              BIGSERIAL PRIMARY KEY,
    request_id      BYTEA NOT NULL UNIQUE,          -- 32 bytes
    operator        TEXT NOT NULL,
    consumer        TEXT NOT NULL,
    endpoint_hash   BYTEA NOT NULL,                 -- 32 bytes
    method          TEXT NOT NULL,
    path_template   TEXT NOT NULL,
    unit_count      BIGINT NOT NULL,
    price_version   INTEGER NOT NULL,
    charged_amount  NUMERIC(39, 0) NOT NULL,        -- i128 range, integer stroops
    protocol        TEXT NOT NULL,                  -- x402 | mpp_charge | mpp_session
    channel         TEXT,                           -- non-null iff mpp_session
    observed_ledger INTEGER NOT NULL,
    period_id       BIGINT REFERENCES periods (id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT requests_request_id_length CHECK (octet_length(request_id) = 32),
    CONSTRAINT requests_endpoint_hash_length CHECK (octet_length(endpoint_hash) = 32),
    CONSTRAINT requests_unit_count_positive CHECK (unit_count > 0),
    CONSTRAINT requests_charged_amount_nonnegative CHECK (charged_amount >= 0),
    CONSTRAINT requests_protocol_check CHECK (protocol IN ('x402', 'mpp_charge', 'mpp_session')),
    CONSTRAINT requests_channel_iff_session CHECK ((protocol = 'mpp_session') = (channel IS NOT NULL))
);

CREATE INDEX ON requests (operator, consumer, observed_ledger);
CREATE INDEX ON requests (period_id);
