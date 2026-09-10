# Recorded Soroban RPC responses

Used by `internal/stellar`'s tests to mock the RPC boundary (§8: "Mock only
the Soroban RPC boundary, and mock it with recorded real responses").

Captured against `https://soroban-testnet.stellar.org` on 2026-09-10 unless
noted otherwise:

| File | Source |
|---|---|
| `get_latest_ledger.json` | Live call. `metadataXdr` truncated (it carries the full ledger close meta, ~170KB) — everything else is the genuine response |
| `get_events.json` | Live call against a real recent ledger range |
| `get_ledger_entries.json` | Live call for a real contract instance entry (contract `CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC`'s instance key) |
| `get_transaction_success.json` | Live call for a real settled testnet transaction |
| `get_transaction_not_found.json` | Live call with a well-formed but non-existent hash |
| `simulate_transaction_error.json` | Live call with a malformed transaction, to capture the error shape |
| `send_transaction_error.json` | Live call with a malformed transaction, to capture the JSON-RPC-level error shape |
| `send_transaction_pending.json` | From the official RPC method reference docs — submitting a real transaction requires a funded, sequenced, signed envelope, out of scope for this fixture |
| `simulate_transaction_success.json` | From the official RPC method reference docs, same reason |
