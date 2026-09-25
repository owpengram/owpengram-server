package donations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

// TestFetchBlockTransactionsSurvivesUnknownTxTypes is the regression test for
// a watcher that stopped dead.
//
// The scanner used ethclient.BlockByNumber, which runs go-ethereum's
// consensus decoder and rejects an entire block with "transaction type not
// supported" on meeting a transaction type it does not know. Base, Optimism
// and Arbitrum use such types in ordinary blocks, so those chains never
// scanned at all; Polygon wedged the first time one appeared and, because
// the cursor only advances after a clean pass, re-hit the same block every
// 5 seconds forever -- a real 5 POL deposit 150 blocks later was never seen.
//
// The fixture block below carries a type-0x7e (OP-stack deposit)
// transaction, an EIP-7702 type-0x04 one and a contract creation alongside
// the plain transfer that matters.
func TestFetchBlockTransactionsSurvivesUnknownTxTypes(t *testing.T) {
	const wantTo = "0xf7954d8030d40339fc4824bb20dd6cc294093677"
	block := map[string]any{
		// A complete header, so the old decoder gets far enough to reach the
		// transactions and fail on their type rather than on a missing field.
		"parentHash":       "0x1111111111111111111111111111111111111111111111111111111111111111",
		"sha3Uncles":       "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
		"miner":            "0x0000000000000000000000000000000000000000",
		"stateRoot":        "0x2222222222222222222222222222222222222222222222222222222222222222",
		"transactionsRoot": "0x3333333333333333333333333333333333333333333333333333333333333333",
		"receiptsRoot":     "0x4444444444444444444444444444444444444444444444444444444444444444",
		"logsBloom":        "0x" + strings.Repeat("00", 256),
		"difficulty":       "0x0",
		"gasLimit":         "0x1c9c380",
		"gasUsed":          "0x5208",
		"timestamp":        "0x66000000",
		"extraData":        "0x",
		"mixHash":          "0x5555555555555555555555555555555555555555555555555555555555555555",
		"nonce":            "0x0000000000000000",
		"baseFeePerGas":    "0x7",
		"size":             "0x100",
		"totalDifficulty":  "0x0",
		"uncles":           []any{},
		"hash":             "0x6666666666666666666666666666666666666666666666666666666666666666",
		"number":           "0x5a0dfb4",
		"transactions": []map[string]any{
			{ // OP-stack deposit transaction: no signature, exotic type
				"hash": "0xa1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1", "type": "0x7e", "from": "0xdeaddeaddeaddeaddeaddeaddeaddeaddead0001",
				"to": "0x4200000000000000000000000000000000000007", "value": "0x0", "input": "0x",
			},
			{ // the deposit we must still find
				"hash": "0xb2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2b2", "type": "0x2", "from": "0x515281812fdf5b0d7be5fb25b823a2ab79e0a621",
				"to": wantTo, "value": "0x4563918244f40000", "input": "0x",
			},
			{ // contract creation: no recipient
				"hash": "0xc3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3c3", "type": "0x0", "from": "0x515281812fdf5b0d7be5fb25b823a2ab79e0a621",
				"to": nil, "value": "0x0", "input": "0x60806040",
			},
			{ // a type this build of go-ethereum may not know at all
				"hash": "0xd4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4d4", "type": "0x4", "from": "0x515281812fdf5b0d7be5fb25b823a2ab79e0a621",
				"to": wantTo, "value": "0x0", "input": "0x",
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result any
		switch req.Method {
		case "eth_getBlockByNumber":
			result = block
		default:
			result = nil
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := ethclient.DialContext(ctx, srv.URL)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	// The old path: proof the fixture really is the shape that broke it.
	_, oldErr := client.BlockByNumber(ctx, nil)
	if oldErr == nil || !strings.Contains(oldErr.Error(), "transaction type not supported") {
		t.Fatalf("BlockByNumber error = %v, want \"transaction type not supported\" -- "+
			"the fixture no longer reproduces the failure this test exists for", oldErr)
	}

	txs, err := fetchBlockTransactions(ctx, client, 94428960)
	if err != nil {
		t.Fatalf("fetchBlockTransactions: %v", err)
	}
	if len(txs) != 4 {
		t.Fatalf("read %d transactions, want all 4", len(txs))
	}
	var found bool
	for _, tx := range txs {
		if tx.To != nil && strings.EqualFold(*tx.To, wantTo) && tx.Value == "0x4563918244f40000" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the 5-unit transfer to %s was not among %+v", wantTo, txs)
	}
}

// TestPublicRPCBlocksStayReadable checks the real endpoints the admin
// panel's presets ship with. The decoder incompatibility that wedged the
// watcher lives outside this repo -- a chain upgrade introduces a new
// transaction type and nothing here changes -- so the only way to notice is
// to ask the chains. Opt-in (network) via TELESRV_TEST_LIVE_CHAINS=1.
func TestPublicRPCBlocksStayReadable(t *testing.T) {
	if os.Getenv("TELESRV_TEST_LIVE_CHAINS") != "1" {
		t.Skip("set TELESRV_TEST_LIVE_CHAINS=1 to hit the real chain RPCs")
	}
	for _, tc := range []struct{ name, url string }{
		{"ethereum", "https://ethereum-rpc.publicnode.com"},
		{"bsc", "https://bsc-rpc.publicnode.com"},
		{"polygon", "https://polygon-bor-rpc.publicnode.com"},
		{"base", "https://base-rpc.publicnode.com"},
		{"arbitrum", "https://arbitrum-one-rpc.publicnode.com"},
		{"optimism", "https://optimism-rpc.publicnode.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client, err := ethclient.DialContext(ctx, tc.url)
			if err != nil {
				t.Skipf("dial %s: %v", tc.url, err)
			}
			defer client.Close()
			head, err := client.BlockNumber(ctx)
			if err != nil {
				t.Skipf("head: %v", err)
			}
			// A short run of recent blocks: one exotic transaction anywhere
			// in it used to take the whole chain's watcher down.
			for bn := head - 4; bn <= head; bn++ {
				if _, err := fetchBlockTransactions(ctx, client, bn); err != nil {
					t.Fatalf("block %d on %s is unreadable: %v", bn, tc.name, err)
				}
			}
		})
	}
}
