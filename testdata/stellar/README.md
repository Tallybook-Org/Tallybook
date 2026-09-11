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

## statement_registry fixtures (internal/stellar/registry.go)

Captured live against the real deployed `statement_registry` contract on
testnet (`CB75TTWGP3TLKEDGA2WOLEVCNKLUX6X5KS47GMWGBHUAVES7J55LY25M`, §4),
using two disposable, friendbot-funded testnet-only keypairs (seeds appear
in `registry_test.go`; they hold no funds worth protecting) acting as
operator and consumer. `registry_anchor_*`, `registry_open_dispute_*`, and
`registry_resolve_dispute_*` are each a full genuine call end to end
(sequence fetch → simulate → sign → submit → poll to a real SUCCESS);
`registry_get_statement*.json`, `registry_get_dispute.json`,
`registry_list_statements.json`, and `registry_verify_usage.json` are
single live reads.

`registry_resolve_dispute_simulate_pass1.json` and `_pass2.json` matter
together, not just individually: this is the dual-authorization call §4
flags as hard (operator *and* consumer must both authorize). The first
pass simulates with no authorization attached, to learn what's needed;
the second re-simulates with the consumer's now-signed authorization
entry already attached. That second pass exists because a first live
attempt at this call — built with only one simulate pass — failed on
submission with "trying to access nonce outside of the footprint": the
resource footprint computed by the auth-less first simulation never
reserved access to the consumer's nonce ledger entry, because that
simulation didn't know the consumer's authorization would exist yet.
**This footprint-consistency issue is the actual, sole cause of the
original failure** — see `internal/stellar/invoke.go`'s doc comments on
`invokeAndSubmit` and `AuthorizeInvocation`. An earlier revision of this
file additionally claimed that CAP-71 `AddressV2` credentials failed
authentication and that classic `SorobanCredentialsTypeSorobanCredentialsAddress`
was a required fix; that claim was wrong and has been retracted. A clean
2×2 test (credential type × footprint consistency, a fresh dispute per
cell) showed both credential types succeed identically once the footprint
is consistent, and both fail identically — same diagnostic — when it
isn't. Credential type predicts nothing. Full reproduction with live
transaction hashes: `/tmp/cap71-finding.md` (session artifact, not
committed to this repository).

A third, separate bug surfaced in the same debugging session and is
unrelated to either of the above: attaching two authorization entries for
the same address to one operation (the unsigned template entry
`simulateTransaction` itself returns, left in place, plus a
separately-built signed one appended alongside it) makes the host
authenticate whichever entry it finds first for that address — if that's
the unsigned one, decoding its `Void` signature as the expected `Vec`
fails with `ScErrorCodeScecUnexpectedType`. Fixed by de-duplicating
authorization entries by address (`mergeAuthEntries` in
`internal/stellar/invoke.go`) rather than concatenating them.


## Event fixtures (internal/stellar/events.go)

| File | Source |
|---|---|
| `events_pricebook_publish.json` | Live `getEvents` call against the real `price_book` contract; includes the exact publish event `pricebook_publish_04_gettransaction_success.json`'s transaction produced |
| `events_statement_registry.json` | Live `getEvents` call against the real `statement_registry` contract; includes the exact anchor/dispute/resolve events the `registry_*` fixtures' transactions produced (two full cycles, seq 2 and seq 3) |

one-way-channel's four events (Open, Close, Withdraw, Refund) have no
fixture here — no live instance could be deployed to emit one; see
channel.go's doc comment. events_test.go covers them via round trip
instead (encode with this package's own ScVal builders, decode, compare),
which confirms internal consistency but not a real on-chain shape.
