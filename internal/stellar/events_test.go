package stellar

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

type rawEvent struct {
	Topic []string `json:"topic"`
	Value string   `json:"value"`
}

func loadEventFixture(t *testing.T, name string) []rawEvent {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "stellar", name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var doc struct {
		Result struct {
			Events []rawEvent `json:"events"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse fixture %s: %v", path, err)
	}
	if len(doc.Result.Events) == 0 {
		t.Fatalf("fixture %s has no events", path)
	}
	return doc.Result.Events
}

func TestMatchesTopic(t *testing.T) {
	events := loadEventFixture(t, "events_pricebook_publish")
	ok, err := MatchesTopic(events[0].Topic, TopicPriceBook, TopicPublish)
	if err != nil {
		t.Fatalf("MatchesTopic returned unexpected error: %v", err)
	}
	if !ok {
		t.Error("MatchesTopic = false for a real price_book publish event's own topics")
	}

	ok, err = MatchesTopic(events[0].Topic, TopicStatement, TopicAnchor)
	if err != nil {
		t.Fatalf("MatchesTopic returned unexpected error: %v", err)
	}
	if ok {
		t.Error("MatchesTopic = true for mismatched topics")
	}

	ok, err = MatchesTopic(events[0].Topic, TopicPriceBook)
	if err != nil {
		t.Fatalf("MatchesTopic returned unexpected error: %v", err)
	}
	if ok {
		t.Error("MatchesTopic = true for a topic list of the wrong length")
	}
}

// TestDecodePriceBookPublishEvent_AgainstRealEvents decodes two genuine
// price_book publish events captured live from testnet — one from this
// package's own pricebook_test.go capture run, one from an earlier
// exploratory capture — cross-checking against known values from each.
func TestDecodePriceBookPublishEvent_AgainstRealEvents(t *testing.T) {
	events := loadEventFixture(t, "events_pricebook_publish")
	if len(events) != 2 {
		t.Fatalf("fixture has %d events, want 2", len(events))
	}

	for _, e := range events {
		ok, err := MatchesTopic(e.Topic, TopicPriceBook, TopicPublish)
		if err != nil {
			t.Fatalf("MatchesTopic returned unexpected error: %v", err)
		}
		if !ok {
			t.Fatalf("event topics %v do not match (price_book, publish)", e.Topic)
		}
	}

	// The second captured event is this package's own registryOperator
	// publishing version 1, effective at ledger 4612165 — the same
	// values TestPriceBook_Publish's fixtures assert on independently.
	ev, err := DecodePriceBookPublishEvent(events[1].Value)
	if err != nil {
		t.Fatalf("DecodePriceBookPublishEvent returned unexpected error: %v", err)
	}
	if ev.Operator != registryOperator {
		t.Errorf("Operator = %q, want %q", ev.Operator, registryOperator)
	}
	if ev.Version != 1 {
		t.Errorf("Version = %d, want 1", ev.Version)
	}
	if ev.EffectiveLedger != 4612165 {
		t.Errorf("EffectiveLedger = %d, want 4612165", ev.EffectiveLedger)
	}
}

// TestStatementEvents_AgainstRealEvents decodes six genuine
// anchor/dispute/resolve events captured live from testnet — two full
// anchor→dispute→resolve cycles this package's own registry_test.go
// fixtures were captured alongside — cross-checking every field against
// values already known from those fixtures.
func TestStatementEvents_AgainstRealEvents(t *testing.T) {
	events := loadEventFixture(t, "events_statement_registry")
	if len(events) != 6 {
		t.Fatalf("fixture has %d events, want 6", len(events))
	}

	var anchors []*StatementAnchorEvent
	var disputes []*StatementDisputeEvent
	var resolves []*StatementResolveEvent

	for i, e := range events {
		switch {
		case mustMatchTopic(t, e.Topic, TopicStatement, TopicAnchor):
			ev, err := DecodeStatementAnchorEvent(e.Value)
			if err != nil {
				t.Fatalf("event %d: DecodeStatementAnchorEvent: %v", i, err)
			}
			anchors = append(anchors, ev)
		case mustMatchTopic(t, e.Topic, TopicStatement, TopicDispute):
			ev, err := DecodeStatementDisputeEvent(e.Value)
			if err != nil {
				t.Fatalf("event %d: DecodeStatementDisputeEvent: %v", i, err)
			}
			disputes = append(disputes, ev)
		case mustMatchTopic(t, e.Topic, TopicStatement, TopicResolve):
			ev, err := DecodeStatementResolveEvent(e.Value)
			if err != nil {
				t.Fatalf("event %d: DecodeStatementResolveEvent: %v", i, err)
			}
			resolves = append(resolves, ev)
		default:
			t.Fatalf("event %d has unrecognized topics %v", i, e.Topic)
		}
	}

	if len(anchors) != 2 || len(disputes) != 2 || len(resolves) != 2 {
		t.Fatalf("got %d anchors, %d disputes, %d resolves, want 2 each", len(anchors), len(disputes), len(resolves))
	}

	// The seq-3 cycle matches registry_test.go's fixtures exactly.
	var anchor3, dispute3, resolve3 bool
	for _, a := range anchors {
		if a.Seq != 3 {
			continue
		}
		anchor3 = true
		if a.Operator != registryOperator {
			t.Errorf("anchor seq 3: Operator = %q, want %q", a.Operator, registryOperator)
		}
		if a.Consumer != registryConsumer {
			t.Errorf("anchor seq 3: Consumer = %q, want %q", a.Consumer, registryConsumer)
		}
		if a.AmountBilled.Cmp(big.NewInt(3000)) != 0 {
			t.Errorf("anchor seq 3: AmountBilled = %s, want 3000", a.AmountBilled)
		}
		if a.Protocol != ProtocolX402 {
			t.Errorf("anchor seq 3: Protocol = %q, want %q", a.Protocol, ProtocolX402)
		}
	}
	for _, d := range disputes {
		if d.Seq != 3 {
			continue
		}
		dispute3 = true
		if d.Consumer != registryConsumer {
			t.Errorf("dispute seq 3: Consumer = %q, want %q", d.Consumer, registryConsumer)
		}
	}
	for _, r := range resolves {
		if r.Seq != 3 {
			continue
		}
		resolve3 = true
		if r.AmountCredited.Cmp(big.NewInt(300)) != 0 {
			t.Errorf("resolve seq 3: AmountCredited = %s, want 300", r.AmountCredited)
		}
	}
	if !anchor3 || !dispute3 || !resolve3 {
		t.Errorf("did not find all of seq-3's anchor/dispute/resolve events: anchor=%v dispute=%v resolve=%v", anchor3, dispute3, resolve3)
	}
}

func mustMatchTopic(t *testing.T, topic []string, want ...string) bool {
	t.Helper()
	ok, err := MatchesTopic(topic, want...)
	if err != nil {
		t.Fatalf("MatchesTopic returned unexpected error: %v", err)
	}
	return ok
}

// The one-way-channel event tests below are round-trip only, not checked
// against a live capture: channel.go's doc comment explains why one
// couldn't be obtained. Each still confirms the field order and types
// this package encodes/decodes match the contract's real struct
// declarations (event.rs) — the topic default (the struct name in
// snake_case) is exercised too, though its correctness rests on
// soroban_sdk's documented default behaviour rather than an observed one.

func TestDecodeChannelOpenEvent_RoundTrip(t *testing.T) {
	from := "GAAACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB7JZX"
	to := "GCWV2YQTXWP72QTU5YX5FM237RCHY6REV6BL6TKBEFCQ5R2QCTX67VUG"
	token := "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	var commitmentKey [32]byte
	for i := range commitmentKey {
		commitmentKey[i] = byte(i)
	}

	fromVal, err := ScvAddress(from)
	if err != nil {
		t.Fatalf("ScvAddress: %v", err)
	}
	toVal, err := ScvAddress(to)
	if err != nil {
		t.Fatalf("ScvAddress: %v", err)
	}
	tokenVal, err := ScvAddress(token)
	if err != nil {
		t.Fatalf("ScvAddress: %v", err)
	}
	amountVal, err := ScvI128(big.NewInt(500))
	if err != nil {
		t.Fatalf("ScvI128: %v", err)
	}

	// Field order matches event.rs's Open struct declaration exactly:
	// from, commitment_key, to, token, amount, refund_waiting_period.
	valueB64, err := MarshalScValBase64(ScvVec([]xdr.ScVal{
		fromVal, ScvBytes(commitmentKey[:]), toVal, tokenVal, amountVal, ScvU32(20),
	}))
	if err != nil {
		t.Fatalf("MarshalScValBase64: %v", err)
	}

	ev, err := DecodeChannelOpenEvent(valueB64)
	if err != nil {
		t.Fatalf("DecodeChannelOpenEvent returned unexpected error: %v", err)
	}
	if ev.From != from || ev.To != to || ev.Token != token {
		t.Errorf("From/To/Token = %q/%q/%q, want %q/%q/%q", ev.From, ev.To, ev.Token, from, to, token)
	}
	if ev.CommitmentKey != commitmentKey {
		t.Errorf("CommitmentKey = %x, want %x", ev.CommitmentKey, commitmentKey)
	}
	if ev.Amount.Cmp(big.NewInt(500)) != 0 {
		t.Errorf("Amount = %s, want 500", ev.Amount)
	}
	if ev.RefundWaitingPeriod != 20 {
		t.Errorf("RefundWaitingPeriod = %d, want 20", ev.RefundWaitingPeriod)
	}
}

func TestDecodeChannelCloseEvent_RoundTrip(t *testing.T) {
	valueB64, err := MarshalScValBase64(ScvVec([]xdr.ScVal{ScvU32(4700123)}))
	if err != nil {
		t.Fatalf("MarshalScValBase64: %v", err)
	}
	ev, err := DecodeChannelCloseEvent(valueB64)
	if err != nil {
		t.Fatalf("DecodeChannelCloseEvent returned unexpected error: %v", err)
	}
	if ev.EffectiveAtLedger != 4700123 {
		t.Errorf("EffectiveAtLedger = %d, want 4700123", ev.EffectiveAtLedger)
	}
}

func TestDecodeChannelWithdrawEvent_RoundTrip(t *testing.T) {
	to := "GCWV2YQTXWP72QTU5YX5FM237RCHY6REV6BL6TKBEFCQ5R2QCTX67VUG"
	toVal, err := ScvAddress(to)
	if err != nil {
		t.Fatalf("ScvAddress: %v", err)
	}
	amountVal, err := ScvI128(big.NewInt(1234))
	if err != nil {
		t.Fatalf("ScvI128: %v", err)
	}
	valueB64, err := MarshalScValBase64(ScvVec([]xdr.ScVal{toVal, amountVal}))
	if err != nil {
		t.Fatalf("MarshalScValBase64: %v", err)
	}

	ev, err := DecodeChannelWithdrawEvent(valueB64)
	if err != nil {
		t.Fatalf("DecodeChannelWithdrawEvent returned unexpected error: %v", err)
	}
	if ev.To != to {
		t.Errorf("To = %q, want %q", ev.To, to)
	}
	if ev.Amount.Cmp(big.NewInt(1234)) != 0 {
		t.Errorf("Amount = %s, want 1234", ev.Amount)
	}
}

func TestDecodeChannelRefundEvent_RoundTrip(t *testing.T) {
	from := "GAAACAQDAQCQMBYIBEFAWDANBYHRAEISCMKBKFQXDAMRUGY4DUPB7JZX"
	fromVal, err := ScvAddress(from)
	if err != nil {
		t.Fatalf("ScvAddress: %v", err)
	}
	amountVal, err := ScvI128(big.NewInt(9999))
	if err != nil {
		t.Fatalf("ScvI128: %v", err)
	}
	valueB64, err := MarshalScValBase64(ScvVec([]xdr.ScVal{fromVal, amountVal}))
	if err != nil {
		t.Fatalf("MarshalScValBase64: %v", err)
	}

	ev, err := DecodeChannelRefundEvent(valueB64)
	if err != nil {
		t.Fatalf("DecodeChannelRefundEvent returned unexpected error: %v", err)
	}
	if ev.From != from {
		t.Errorf("From = %q, want %q", ev.From, from)
	}
	if ev.Amount.Cmp(big.NewInt(9999)) != 0 {
		t.Errorf("Amount = %s, want 9999", ev.Amount)
	}
}

func TestDecodeEventFields_RejectsWrongLength(t *testing.T) {
	valueB64, err := MarshalScValBase64(ScvVec([]xdr.ScVal{ScvU32(1)}))
	if err != nil {
		t.Fatalf("MarshalScValBase64: %v", err)
	}
	if _, err := decodeEventFields(valueB64, 2); err == nil {
		t.Error("decodeEventFields accepted a value with the wrong number of fields")
	}
}
