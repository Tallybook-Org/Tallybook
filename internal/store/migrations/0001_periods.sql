-- periods: the billing-period aggregation unit requests roll up into before
-- being anchored as a statement. Not given verbatim in the system prompt's
-- schema section (which defers periods/statements/settlements/chain_events
-- to "see build sequence") — designed here to support what later steps need:
-- exactly one open period per (operator, consumer, protocol, channel) at a
-- time, and a period closed whenever the price book version changes, not
-- only at calendar period end (statement_registry.anchor rejects a
-- statement whose period spans a price change).
CREATE TABLE periods (
    id            BIGSERIAL PRIMARY KEY,
    operator      TEXT NOT NULL,
    consumer      TEXT NOT NULL,
    protocol      TEXT NOT NULL,               -- x402 | mpp_charge | mpp_session
    channel       TEXT,                        -- non-null iff mpp_session
    price_version INTEGER NOT NULL,
    period_start  INTEGER NOT NULL,             -- ledger
    period_end    INTEGER,                      -- ledger; null while open
    status        TEXT NOT NULL DEFAULT 'open', -- open | closed | anchored
    statement_seq BIGINT,                       -- set once anchored on chain
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at     TIMESTAMPTZ,

    CONSTRAINT periods_protocol_check CHECK (protocol IN ('x402', 'mpp_charge', 'mpp_session')),
    CONSTRAINT periods_channel_iff_session CHECK ((protocol = 'mpp_session') = (channel IS NOT NULL)),
    CONSTRAINT periods_status_check CHECK (status IN ('open', 'closed', 'anchored')),
    CONSTRAINT periods_period_end_after_start CHECK (period_end IS NULL OR period_end >= period_start),
    CONSTRAINT periods_closed_fields CHECK (status = 'open' OR (period_end IS NOT NULL AND closed_at IS NOT NULL))
);

-- At most one open period per scope. COALESCE collapses the non-session
-- NULL channel to '' because a plain unique index treats NULL as distinct
-- from NULL, which would otherwise let multiple x402/mpp_charge periods
-- for the same operator+consumer stay open at once.
CREATE UNIQUE INDEX periods_one_open_per_scope ON periods (operator, consumer, protocol, COALESCE(channel, ''))
    WHERE status = 'open';

CREATE INDEX ON periods (operator, consumer, period_start);
