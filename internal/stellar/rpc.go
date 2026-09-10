// Package stellar is Tallybook's boundary with the Stellar network: a thin
// Soroban JSON-RPC client, XDR/ScVal helpers, and typed bindings for the
// three contracts described in CLAUDE.md §4.
package stellar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// defaultTimeout bounds every RPC call made with a Client constructed
// without an explicit *http.Client. Every function in this package that
// does I/O takes a context and honours cancellation, but a caller that
// forgets a deadline on ctx should still not hang forever.
const defaultTimeout = 30 * time.Second

// Client is a thin JSON-RPC client for a Soroban RPC node. There is no
// official Go SDK for this API (CLAUDE.md §3) — every method and shape
// below was verified against the live RPC reference and, for six of them,
// against a running testnet node (see testdata/stellar/README.md) before
// being implemented, rather than assumed from memory.
type Client struct {
	url        string
	httpClient *http.Client
	nextID     atomic.Int64
}

// NewClient returns a Client that sends requests to url. If httpClient is
// nil, a client with defaultTimeout is used; pass one explicitly to
// control timeouts, retries, or transport behaviour yourself.
func NewClient(url string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{url: url, httpClient: httpClient}
}

// rpcRequest is the JSON-RPC 2.0 envelope every call sends.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// rpcResponse is the JSON-RPC 2.0 envelope every call receives. Result is
// left as raw JSON so call can defer decoding it to the method-specific
// result type only once Error has been checked.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is a JSON-RPC 2.0 error object — the protocol-level error shape
// used for malformed requests (e.g. bad XDR), distinct from a method's own
// "error" field in its result (which simulateTransaction uses for a
// simulation failure that isn't a protocol error).
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("soroban rpc: code %d: %s", e.Code, e.Message)
}

// call sends method with params and decodes the result into out. out may be
// nil for a method with no meaningful result. On an RPC-level error, call
// returns an error wrapping *RPCError, matchable with errors.As.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	id := c.nextID.Add(1)
	reqBody, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return fmt.Errorf("stellar: marshal %s request: %w", method, err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("stellar: build %s request: %w", method, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("stellar: %s: request failed: %w", method, err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("stellar: %s: unexpected HTTP status %d: %s", method, httpResp.StatusCode, string(body))
	}

	var resp rpcResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return fmt.Errorf("stellar: %s: decode response envelope: %w", method, err)
	}
	if resp.Error != nil {
		return fmt.Errorf("stellar: %s: %w", method, resp.Error)
	}
	if out != nil {
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("stellar: %s: decode result: %w", method, err)
		}
	}
	return nil
}

// GetLatestLedgerResult is getLatestLedger's result.
type GetLatestLedgerResult struct {
	ID              string `json:"id"`
	ProtocolVersion uint32 `json:"protocolVersion"`
	Sequence        uint32 `json:"sequence"`
	CloseTime       string `json:"closeTime"` // unix timestamp
	HeaderXDR       string `json:"headerXdr"`
	MetadataXDR     string `json:"metadataXdr"`
}

// GetLatestLedger returns the most recent ledger the RPC node knows about.
// Takes no parameters.
func (c *Client) GetLatestLedger(ctx context.Context) (*GetLatestLedgerResult, error) {
	var result GetLatestLedgerResult
	if err := c.call(ctx, "getLatestLedger", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EventFilter narrows a getEvents call to specific contracts, event types,
// and topics.
type EventFilter struct {
	Type        string     `json:"type,omitempty"` // "contract" | "system"
	ContractIDs []string   `json:"contractIds,omitempty"`
	Topics      [][]string `json:"topics,omitempty"`
}

// EventsPagination pages through a getEvents result. Cursor and Limit are
// independent of the StartLedger/EndLedger on GetEventsParams — the RPC API
// takes either a ledger range or a cursor, not both (CLAUDE.md doesn't
// enforce this client-side; passing both is a caller error the RPC node
// itself will reject).
type EventsPagination struct {
	Cursor string `json:"cursor,omitempty"`
	Limit  uint32 `json:"limit,omitempty"`
}

// GetEventsParams is getEvents' request.
type GetEventsParams struct {
	StartLedger uint32            `json:"startLedger,omitempty"`
	EndLedger   uint32            `json:"endLedger,omitempty"`
	Filters     []EventFilter     `json:"filters,omitempty"`
	Pagination  *EventsPagination `json:"pagination,omitempty"`
	XDRFormat   string            `json:"xdrFormat,omitempty"`
}

// EventInfo is one contract or system event returned by getEvents.
type EventInfo struct {
	Type                     string   `json:"type"`
	Ledger                   uint32   `json:"ledger"`
	LedgerClosedAt           string   `json:"ledgerClosedAt"`
	ContractID               string   `json:"contractId"`
	ID                       string   `json:"id"`
	TransactionIndex         uint32   `json:"transactionIndex"`
	OperationIndex           uint32   `json:"operationIndex"`
	InSuccessfulContractCall bool     `json:"inSuccessfulContractCall"`
	Topic                    []string `json:"topic"` // base64 ScVal per entry
	Value                    string   `json:"value"` // base64 ScVal
	TxHash                   string   `json:"txHash"`
}

// GetEventsResult is getEvents' result.
type GetEventsResult struct {
	LatestLedger          uint32      `json:"latestLedger"`
	OldestLedger          uint32      `json:"oldestLedger"`
	LatestLedgerCloseTime string      `json:"latestLedgerCloseTime"`
	OldestLedgerCloseTime string      `json:"oldestLedgerCloseTime"`
	Events                []EventInfo `json:"events"`
	Cursor                string      `json:"cursor"`
}

// GetEvents returns contract and system events matching params.
func (c *Client) GetEvents(ctx context.Context, params GetEventsParams) (*GetEventsResult, error) {
	var result GetEventsResult
	if err := c.call(ctx, "getEvents", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// LedgerEntryResult is one entry returned by getLedgerEntries.
type LedgerEntryResult struct {
	Key                   string `json:"key"` // base64 LedgerKey
	XDR                   string `json:"xdr"` // base64 LedgerEntryData
	LastModifiedLedgerSeq uint32 `json:"lastModifiedLedgerSeq"`
	LiveUntilLedgerSeq    uint32 `json:"liveUntilLedgerSeq,omitempty"`
	ExtXDR                string `json:"extXdr,omitempty"`
}

// GetLedgerEntriesResult is getLedgerEntries' result.
type GetLedgerEntriesResult struct {
	Entries      []LedgerEntryResult `json:"entries"`
	LatestLedger uint32              `json:"latestLedger"`
}

// GetLedgerEntries returns the current ledger entries for keys (base64
// LedgerKey XDR, at most 200 per the RPC's own limit — not enforced here).
func (c *Client) GetLedgerEntries(ctx context.Context, keys []string) (*GetLedgerEntriesResult, error) {
	var result GetLedgerEntriesResult
	params := struct {
		Keys []string `json:"keys"`
	}{Keys: keys}
	if err := c.call(ctx, "getLedgerEntries", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// SimulateResourceConfig adjusts resource estimation for simulateTransaction.
type SimulateResourceConfig struct {
	InstructionLeeway uint64 `json:"instructionLeeway,omitempty"`
}

// SimulateTransactionParams is simulateTransaction's request. Transaction
// must be a base64-encoded TransactionEnvelope with a single
// invokeHostFunction operation — the RPC node rejects anything else.
type SimulateTransactionParams struct {
	Transaction    string                  `json:"transaction"`
	ResourceConfig *SimulateResourceConfig `json:"resourceConfig,omitempty"`
	XDRFormat      string                  `json:"xdrFormat,omitempty"`
	AuthMode       string                  `json:"authMode,omitempty"`
}

// SimulateHostFunctionResult is one entry of simulateTransaction's Results.
type SimulateHostFunctionResult struct {
	Auth []string `json:"auth"` // base64 SorobanAuthorizationEntry per entry
	XDR  string   `json:"xdr"`  // base64 ScVal, the invocation's return value
}

// RestorePreamble is populated when a simulated invocation touched an
// archived ledger entry that must be restored before the real invocation
// can succeed.
type RestorePreamble struct {
	MinResourceFee  string `json:"minResourceFee"`
	TransactionData string `json:"transactionData"`
}

// LedgerEntryChange is one entry of simulateTransaction's StateChanges.
type LedgerEntryChange struct {
	Type   string  `json:"type"` // "created" | "updated" | "deleted"
	Key    string  `json:"key"`  // base64 LedgerKey
	Before *string `json:"before"`
	After  *string `json:"after"`
}

// SimulateCost is simulateTransaction's resource cost estimate.
type SimulateCost struct {
	CPUInsns string `json:"cpuInsns"`
	MemBytes string `json:"memBytes"`
}

// SimulateTransactionResult is simulateTransaction's result. Error is set
// when the simulation itself failed (e.g. the contract call would trap) —
// this is not a JSON-RPC-level error, so callers must check it explicitly;
// custody's commitment verification (internal/custody, a later package)
// depends on distinguishing "simulation ran and failed" from "simulation
// could not run at all".
type SimulateTransactionResult struct {
	LatestLedger    uint32                       `json:"latestLedger"`
	MinResourceFee  string                       `json:"minResourceFee,omitempty"`
	TransactionData string                       `json:"transactionData,omitempty"`
	Results         []SimulateHostFunctionResult `json:"results,omitempty"`
	Events          []string                     `json:"events,omitempty"`
	RestorePreamble *RestorePreamble             `json:"restorePreamble,omitempty"`
	StateChanges    []LedgerEntryChange          `json:"stateChanges,omitempty"`
	Cost            *SimulateCost                `json:"cost,omitempty"`
	Error           string                       `json:"error,omitempty"`
}

// SimulateTransaction submits a trial invocation and returns its predicted
// effects and resource cost, without submitting it to the network.
func (c *Client) SimulateTransaction(ctx context.Context, params SimulateTransactionParams) (*SimulateTransactionResult, error) {
	var result SimulateTransactionResult
	if err := c.call(ctx, "simulateTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// sendTransaction status values (SendTransactionResult.Status).
const (
	SendTransactionStatusPending   = "PENDING"
	SendTransactionStatusDuplicate = "DUPLICATE"
	SendTransactionStatusTryAgain  = "TRY_AGAIN_LATER"
	SendTransactionStatusError     = "ERROR"
)

// SendTransactionResult is sendTransaction's result. A PENDING status means
// only that the node accepted the transaction for inclusion — the caller
// must poll GetTransaction to learn the outcome.
type SendTransactionResult struct {
	Hash                  string   `json:"hash"`
	Status                string   `json:"status"`
	LatestLedger          uint32   `json:"latestLedger"`
	LatestLedgerCloseTime string   `json:"latestLedgerCloseTime"`
	ErrorResultXDR        string   `json:"errorResultXdr,omitempty"`
	DiagnosticEventsXDR   []string `json:"diagnosticEventsXdr,omitempty"`
}

// SendTransaction submits a signed, base64-encoded TransactionEnvelope to
// the network.
func (c *Client) SendTransaction(ctx context.Context, transactionXDR string) (*SendTransactionResult, error) {
	var result SendTransactionResult
	params := struct {
		Transaction string `json:"transaction"`
	}{Transaction: transactionXDR}
	if err := c.call(ctx, "sendTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// getTransaction status values (GetTransactionResult.Status).
const (
	GetTransactionStatusSuccess  = "SUCCESS"
	GetTransactionStatusNotFound = "NOT_FOUND"
	GetTransactionStatusFailed   = "FAILED"
)

// TransactionEvents holds a transaction's events, split by kind. Both
// slices may be empty even for a SUCCESS transaction.
type TransactionEvents struct {
	TransactionEventsXDR []string   `json:"transactionEventsXdr,omitempty"`
	ContractEventsXDR    [][]string `json:"contractEventsXdr,omitempty"`
}

// GetTransactionResult is getTransaction's result. The fields beyond Status
// and TxHash are populated only once Status is SUCCESS or FAILED — the
// live RPC node returns them present-but-zero-valued for NOT_FOUND rather
// than omitting them, so callers must gate on Status, not on whether a
// field is the zero value.
type GetTransactionResult struct {
	Status                string            `json:"status"`
	TxHash                string            `json:"txHash"`
	LatestLedger          uint32            `json:"latestLedger"`
	LatestLedgerCloseTime string            `json:"latestLedgerCloseTime"`
	OldestLedger          uint32            `json:"oldestLedger"`
	OldestLedgerCloseTime string            `json:"oldestLedgerCloseTime"`
	Ledger                uint32            `json:"ledger"`
	CreatedAt             string            `json:"createdAt"`
	ApplicationOrder      uint32            `json:"applicationOrder"`
	FeeBump               bool              `json:"feeBump"`
	EnvelopeXDR           string            `json:"envelopeXdr,omitempty"`
	ResultXDR             string            `json:"resultXdr,omitempty"`
	ResultMetaXDR         string            `json:"resultMetaXdr,omitempty"`
	DiagnosticEventsXDR   []string          `json:"diagnosticEventsXdr,omitempty"`
	Events                TransactionEvents `json:"events,omitempty"`
}

// GetTransaction returns the status and, once settled, the result of the
// transaction with the given hex-encoded hash.
func (c *Client) GetTransaction(ctx context.Context, hash string) (*GetTransactionResult, error) {
	var result GetTransactionResult
	params := struct {
		Hash string `json:"hash"`
	}{Hash: hash}
	if err := c.call(ctx, "getTransaction", params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
