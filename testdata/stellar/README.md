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

## Contract binding fixtures (internal/stellar/pricebook.go etc.)

Captured live against the real deployed `price_book` contract on testnet
(`CB2IEP4SQ2GC5747HFHNMXEYWEULC5Z5TTTLET2QA4CAA5SYCWAXFKAW`, §4), using a
real operator address with real published versions (found via a live
`getEvents` call filtered to that contract's `publish` events):

| File | Source |
|---|---|
| `pricebook_latest.json`, `pricebook_get_version.json`, `pricebook_version_at.json` | Live `simulateTransaction` calls reading that real operator's real published price book |
| `pricebook_latest_not_found.json` | Live call for an operator with no published price book — captures the contract's real `NotFound` (error discriminant 2) failure shape |
| `pricebook_publish_01_getledgerentries_account.json` through `_04_gettransaction_success.json` | A genuine successful `publish` call end to end (sequence fetch → simulate → sign → submit → poll), signed by a disposable, friendbot-funded testnet-only keypair created solely for this capture. Its seed appears in `pricebook_test.go`; it holds no funds worth protecting and authorizes nothing beyond this one already-captured call |
