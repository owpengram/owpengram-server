package donations

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"

	"telesrv/internal/domain"
	"telesrv/internal/store"
)

// sweepFakeStore implements only the handful of methods a sweep touches.
// The embedded interface supplies the rest; a sweep that starts calling one
// of them panics loudly rather than silently passing.
type sweepFakeStore struct {
	store.DonationStore
	mu          sync.Mutex
	seed, nonce []byte
	chain       domain.DonationChain
	addresses   []domain.DonationAddress
}

func (f *sweepFakeStore) WalletSeed(context.Context) ([]byte, []byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seed, f.nonce, f.seed != nil, nil
}

func (f *sweepFakeStore) CreateWalletSeed(_ context.Context, seed, nonce []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seed, f.nonce = seed, nonce
	return nil
}

func (f *sweepFakeStore) DonationChain(_ context.Context, key string) (domain.DonationChain, bool, error) {
	if key != f.chain.Key {
		return domain.DonationChain{}, false, nil
	}
	return f.chain, true, nil
}

func (f *sweepFakeStore) DonationTokens(context.Context, string) ([]domain.DonationToken, error) {
	return nil, nil
}

func (f *sweepFakeStore) ListDonationAddresses(context.Context) ([]domain.DonationAddress, error) {
	return f.addresses, nil
}

// fakeNode is a JSON-RPC node with one funded address that already has a
// transaction stuck in its mempool: the mined nonce is 0, the pending nonce
// is 1.
type fakeNode struct {
	chainID  int64
	gasPrice *big.Int
	balance  *big.Int

	mu   sync.Mutex
	sent []*types.Transaction
}

func (n *fakeNode) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params []any           `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		var result any
		var rpcErr any
		switch req.Method {
		case "eth_chainId", "net_version":
			result = hexutil.EncodeUint64(uint64(n.chainID))
		case "eth_gasPrice":
			result = hexutil.EncodeBig(n.gasPrice)
		case "eth_getBalance":
			result = hexutil.EncodeBig(n.balance)
		case "eth_getTransactionCount":
			tag, _ := req.Params[1].(string)
			if tag == "pending" {
				result = "0x1" // one transaction already queued
			} else {
				result = "0x0" // nothing mined yet
			}
		case "eth_sendRawTransaction":
			raw, _ := req.Params[0].(string)
			data, err := hexutil.Decode(raw)
			if err != nil {
				rpcErr = map[string]any{"code": -32000, "message": err.Error()}
				break
			}
			tx := new(types.Transaction)
			if err := tx.UnmarshalBinary(data); err != nil {
				rpcErr = map[string]any{"code": -32000, "message": err.Error()}
				break
			}
			n.mu.Lock()
			n.sent = append(n.sent, tx)
			n.mu.Unlock()
			result = tx.Hash().Hex()
		default:
			rpcErr = map[string]any{"code": -32601, "message": "unsupported in this fake: " + req.Method}
		}
		w.Header().Set("Content-Type", "application/json")
		out := map[string]any{"jsonrpc": "2.0", "id": req.ID}
		if rpcErr != nil {
			out["error"] = rpcErr
		} else {
			out["result"] = result
		}
		_ = json.NewEncoder(w).Encode(out)
	}
}

// TestSweepReplacesAStuckTransaction pins the recovery path for a sweep that
// is already stuck in the mempool.
//
// A sweep sends an address's entire balance, so two queued sweeps can never
// both be funded. Starting from the PENDING nonce queued a second one behind
// the first, and the node rejected it every single time:
//
//	insufficient funds for gas * price + value: balance 9000000000000000000,
//	queued cost 9000000000000000000, tx cost 9000000000000000000,
//	overshot 9000000000000000000
//
// which left the address permanently unsweepable -- each retry added another
// doomed transaction. The run must reuse the stuck nonce so the new
// transaction REPLACES it, and must outbid it, or the node refuses the
// replacement as underpriced.
func TestSweepReplacesAStuckTransaction(t *testing.T) {
	ctx := context.Background()
	const gwei = 1_000_000_000

	node := &fakeNode{
		chainID:  137,
		gasPrice: big.NewInt(100 * gwei),
		balance:  new(big.Int).Mul(big.NewInt(9), big.NewInt(1e18)),
	}
	srv := httptest.NewServer(node.handler())
	defer srv.Close()

	st := &sweepFakeStore{chain: domain.DonationChain{
		Key: "polygon", Name: "Polygon", ChainID: 137, NativeSymbol: "POL", NativeDecimals: 18,
		RPCURL: srv.URL, ConfirmationsRequired: 20, ManualUSDRateMicros: 113_580, Enabled: true,
	}}
	key, err := ParseEncryptionKey(testSweepWalletKey)
	if err != nil {
		t.Fatalf("parse key: %v", err)
	}
	svc, err := NewService(ctx, st, key)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	if _, err := svc.EnsureWallet(ctx); err != nil {
		t.Fatalf("ensure wallet: %v", err)
	}
	from, err := svc.wallet.DeriveAddress(0)
	if err != nil {
		t.Fatalf("derive address: %v", err)
	}
	st.addresses = []domain.DonationAddress{{UserID: 4242, DerivationIndex: 0, Address: from}}

	const destination = "0x1111111111111111111111111111111111111111"
	if _, err := svc.Sweep(ctx, "polygon", destination); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	node.mu.Lock()
	sent := append([]*types.Transaction(nil), node.sent...)
	node.mu.Unlock()
	if len(sent) != 1 {
		t.Fatalf("broadcast %d transactions, want 1", len(sent))
	}
	tx := sent[0]

	if tx.Nonce() != 0 {
		t.Fatalf("broadcast nonce = %d, want 0 (the stuck transaction's own nonce, so this replaces it "+
			"instead of queueing behind a transaction that already spends the whole balance)", tx.Nonce())
	}
	if tx.GasPrice().Cmp(node.gasPrice) <= 0 {
		t.Fatalf("broadcast gas price = %s, want more than the %s suggestion: a replacement that does not "+
			"outbid the transaction it replaces is refused as underpriced", tx.GasPrice(), node.gasPrice)
	}
	// The value must leave room for this transaction's own gas at the price
	// it actually carries, not at the unbumped suggestion.
	gasCost := new(big.Int).Mul(tx.GasPrice(), new(big.Int).SetUint64(tx.Gas()))
	total := new(big.Int).Add(tx.Value(), gasCost)
	if total.Cmp(node.balance) > 0 {
		t.Fatalf("value %s + gas %s = %s exceeds the %s balance", tx.Value(), gasCost, total, node.balance)
	}
	if !strings.EqualFold(tx.To().Hex(), destination) {
		t.Fatalf("swept to %s, want %s", tx.To().Hex(), destination)
	}
}

const testSweepWalletKey = "5f0e4c3a2b1908877665544332211000ffeeddccbbaa99887766554433221100"
