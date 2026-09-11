package stellar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
)

const realRegistryContractID = "CB75TTWGP3TLKEDGA2WOLEVCNKLUX6X5KS47GMWGBHUAVES7J55LY25M"

// registryOperator and registryConsumer are the two disposable,
// friendbot-funded testnet keypairs the registry_* fixtures were captured
// against — see testdata/stellar/README.md.
const (
	registryOperatorSeed = "SAQJHZ3ZUNSHQB2CWYRGD5JKTJW6YDSCGIFOKHAPESG3DCURKQ4ZTZYM"
	registryConsumerSeed = "SB4A2UA44VMYI2Z6XZPMLRF4XNYZRFMP2IVUVI6KQP5OEV2RGRXHPLT7"
	registryOperator     = "GBUSXHXV53RH2DXO4OJZXJY7AULCBFFO6DHGAAL6XK77BG5V2NSE253A"
	registryConsumer     = "GCWV2YQTXWP72QTU5YX5FM237RCHY6REV6BL6TKBEFCQ5R2QCTX67VUG"
)

// sequencedFixtureServer replays fixtures[method] in order for each
// successive request to that method, repeating the last one once
// exhausted (the shape polling needs: several NOT_FOUND-equivalent calls
// then a final settled one — though these fixture sets only include the
// final response, since that's all these tests assert on).
func sequencedFixtureServer(t *testing.T, fixtures map[string][]string) *httptest.Server {
	t.Helper()
	data := make(map[string][][]byte, len(fixtures))
	for method, names := range fixtures {
		for _, name := range names {
			data[method] = append(data[method], loadFixtureRaw(t, name))
		}
	}
	pos := make(map[string]int, len(fixtures))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		responses, ok := data[req.Method]
		if !ok || len(responses) == 0 {
			http.Error(w, "no fixture routed for method "+req.Method, http.StatusInternalServerError)
			return
		}
		i := pos[req.Method]
		if i >= len(responses) {
			i = len(responses) - 1
		}
		pos[req.Method] = i + 1
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responses[i])
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestStatementRegistry_Anchor(t *testing.T) {
	srv := sequencedFixtureServer(t, map[string][]string{
		"getLedgerEntries":    {"registry_anchor_getledgerentries"},
		"simulateTransaction": {"registry_anchor_simulate"},
		"sendTransaction":     {"registry_anchor_send"},
		"getTransaction":      {"registry_anchor_gettransaction"},
	})
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "Test SDF Network ; September 2015")
	signer, err := keypair.ParseFull(registryOperatorSeed)
	if err != nil {
		t.Fatalf("parse signer: %v", err)
	}

	usageRoot := sha256.Sum256([]byte("registry-capture-usage-root"))
	seq, hash, err := registry.Anchor(context.Background(), signer, registryConsumer,
		4612165, 4612167, usageRoot, 9, registryOperator,
		big.NewInt(3000), big.NewInt(3000), 1, ProtocolX402, nil)
	if err != nil {
		t.Fatalf("Anchor returned unexpected error: %v", err)
	}
	if seq != 3 {
		t.Errorf("seq = %d, want 3 (the real on-chain value)", seq)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
}

func TestStatementRegistry_GetStatement(t *testing.T) {
	srv := singleFixtureServer(t, "registry_get_statement")
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "")

	stmt, err := registry.GetStatement(context.Background(), registryOperator, 3)
	if err != nil {
		t.Fatalf("GetStatement returned unexpected error: %v", err)
	}
	if stmt.Operator != registryOperator {
		t.Errorf("Operator = %q", stmt.Operator)
	}
	if stmt.Consumer != registryConsumer {
		t.Errorf("Consumer = %q", stmt.Consumer)
	}
	if stmt.PeriodStart != 4612165 || stmt.PeriodEnd != 4612167 {
		t.Errorf("period = [%d, %d], want [4612165, 4612167]", stmt.PeriodStart, stmt.PeriodEnd)
	}
	if stmt.RequestCount != 9 {
		t.Errorf("RequestCount = %d, want 9", stmt.RequestCount)
	}
	if stmt.AmountBilled.Cmp(big.NewInt(3000)) != 0 {
		t.Errorf("AmountBilled = %s, want 3000", stmt.AmountBilled)
	}
	if stmt.AmountSettled.Cmp(big.NewInt(3000)) != 0 {
		t.Errorf("AmountSettled = %s, want 3000", stmt.AmountSettled)
	}
	if stmt.PriceBookVersion != 1 {
		t.Errorf("PriceBookVersion = %d, want 1", stmt.PriceBookVersion)
	}
	if stmt.Protocol != ProtocolX402 {
		t.Errorf("Protocol = %q, want %q", stmt.Protocol, ProtocolX402)
	}
	if stmt.Channel != nil {
		t.Errorf("Channel = %v, want nil (protocol is X402)", stmt.Channel)
	}
	if stmt.Status != StatusAnchored {
		t.Errorf("Status = %q, want %q", stmt.Status, StatusAnchored)
	}
	wantRoot := sha256.Sum256([]byte("registry-capture-usage-root"))
	if stmt.UsageRoot != wantRoot {
		t.Errorf("UsageRoot = %x, want %x", stmt.UsageRoot, wantRoot)
	}
}

func TestStatementRegistry_GetStatement_Resolved(t *testing.T) {
	srv := singleFixtureServer(t, "registry_get_statement_resolved")
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "")

	stmt, err := registry.GetStatement(context.Background(), registryOperator, 3)
	if err != nil {
		t.Fatalf("GetStatement returned unexpected error: %v", err)
	}
	if stmt.Status != StatusResolved {
		t.Errorf("Status = %q, want %q", stmt.Status, StatusResolved)
	}
}

func TestStatementRegistry_ListStatements(t *testing.T) {
	srv := singleFixtureServer(t, "registry_list_statements")
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "")

	seqs, err := registry.ListStatements(context.Background(), registryOperator, registryConsumer)
	if err != nil {
		t.Fatalf("ListStatements returned unexpected error: %v", err)
	}
	want := []uint64{1, 2, 3}
	if len(seqs) != len(want) {
		t.Fatalf("got %v, want %v", seqs, want)
	}
	for i := range want {
		if seqs[i] != want[i] {
			t.Errorf("seqs[%d] = %d, want %d", i, seqs[i], want[i])
		}
	}
}

func TestStatementRegistry_VerifyUsage(t *testing.T) {
	srv := singleFixtureServer(t, "registry_verify_usage")
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "")

	usageRoot := sha256.Sum256([]byte("registry-capture-usage-root"))
	ok, err := registry.VerifyUsage(context.Background(), registryOperator, 3, usageRoot, nil)
	if err != nil {
		t.Fatalf("VerifyUsage returned unexpected error: %v", err)
	}
	if !ok {
		t.Error("VerifyUsage = false, want true (leaf equals the usage_root, empty proof)")
	}
}

func TestStatementRegistry_OpenDispute(t *testing.T) {
	srv := sequencedFixtureServer(t, map[string][]string{
		"getLedgerEntries":    {"registry_open_dispute_getledgerentries"},
		"simulateTransaction": {"registry_open_dispute_simulate"},
		"sendTransaction":     {"registry_open_dispute_send"},
		"getTransaction":      {"registry_open_dispute_gettransaction"},
	})
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "Test SDF Network ; September 2015")
	consumerSigner, err := keypair.ParseFull(registryConsumerSeed)
	if err != nil {
		t.Fatalf("parse signer: %v", err)
	}

	reasonHash := sha256.Sum256([]byte("registry-capture-reason"))
	hash, err := registry.OpenDispute(context.Background(), consumerSigner, registryOperator, 3, reasonHash)
	if err != nil {
		t.Fatalf("OpenDispute returned unexpected error: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
}

func TestStatementRegistry_GetDispute(t *testing.T) {
	srv := singleFixtureServer(t, "registry_get_dispute")
	registry := NewStatementRegistry(NewClient(srv.URL, nil), realRegistryContractID, "")

	dispute, err := registry.GetDispute(context.Background(), registryOperator, 3)
	if err != nil {
		t.Fatalf("GetDispute returned unexpected error: %v", err)
	}
	if dispute.Consumer != registryConsumer {
		t.Errorf("Consumer = %q", dispute.Consumer)
	}
	wantReason := sha256.Sum256([]byte("registry-capture-reason"))
	if dispute.ReasonHash != wantReason {
		t.Errorf("ReasonHash = %x, want %x", dispute.ReasonHash, wantReason)
	}
	if dispute.ResolutionHash != nil {
		t.Errorf("ResolutionHash = %v, want nil (not yet resolved)", dispute.ResolutionHash)
	}
	if dispute.ResolvedLedger != nil {
		t.Errorf("ResolvedLedger = %v, want nil (not yet resolved)", dispute.ResolvedLedger)
	}
	if dispute.AmountCredited.Sign() != 0 {
		t.Errorf("AmountCredited = %s, want 0", dispute.AmountCredited)
	}
}

// TestStatementRegistry_ResolveDispute is the dual-authorization case §4
// calls out explicitly. It replays the exact live-captured flow: two
// simulateTransaction passes (the second re-simulates with the consumer's
// authorization already attached, so the resulting resource footprint
// accounts for the consumer's nonce access — the fix this package needed
// after a first live attempt failed with "trying to access nonce outside
// of the footprint" when that second pass was missing), then send and
// poll to a real SUCCESS.
func TestStatementRegistry_ResolveDispute(t *testing.T) {
	srv := sequencedFixtureServer(t, map[string][]string{
		"getLatestLedger":     {"registry_resolve_dispute_getlatestledger"},
		"getLedgerEntries":    {"registry_resolve_dispute_getledgerentries"},
		"simulateTransaction": {"registry_resolve_dispute_simulate_pass1", "registry_resolve_dispute_simulate_pass2"},
		"sendTransaction":     {"registry_resolve_dispute_send"},
		"getTransaction":      {"registry_resolve_dispute_gettransaction"},
	})
	client := NewClient(srv.URL, nil)
	registry := NewStatementRegistry(client, realRegistryContractID, "Test SDF Network ; September 2015")

	opSigner, err := keypair.ParseFull(registryOperatorSeed)
	if err != nil {
		t.Fatalf("parse operator signer: %v", err)
	}
	csSigner, err := keypair.ParseFull(registryConsumerSeed)
	if err != nil {
		t.Fatalf("parse consumer signer: %v", err)
	}

	latest, err := client.GetLatestLedger(context.Background())
	if err != nil {
		t.Fatalf("GetLatestLedger returned unexpected error: %v", err)
	}

	resolutionHash := sha256.Sum256([]byte("registry-capture-resolution"))
	amountCredited := big.NewInt(300)
	args, err := ResolveDisputeArgs(registryOperator, 3, resolutionHash, amountCredited)
	if err != nil {
		t.Fatalf("ResolveDisputeArgs returned unexpected error: %v", err)
	}
	consumerAuth, err := AuthorizeInvocation("Test SDF Network ; September 2015", csSigner, realRegistryContractID, "resolve_dispute", args, latest.Sequence)
	if err != nil {
		t.Fatalf("AuthorizeInvocation returned unexpected error: %v", err)
	}

	hash, err := registry.ResolveDispute(context.Background(), opSigner, 3, resolutionHash, amountCredited, consumerAuth)
	if err != nil {
		t.Fatalf("ResolveDispute returned unexpected error: %v", err)
	}
	if hash == "" {
		t.Error("hash should not be empty")
	}
}
