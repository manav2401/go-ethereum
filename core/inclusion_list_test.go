package core

import (
	"crypto/ecdsa"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/assert"
)

func transaction(nonce uint64, gaslimit uint64, gasPrice *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	return pricedTransaction(nonce, gaslimit, gasPrice, key)
}

func pricedTransaction(nonce uint64, gaslimit uint64, gasprice *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	tx, _ := types.SignTx(types.NewTransaction(nonce, common.Address{}, big.NewInt(100), gaslimit, gasprice, nil), types.HomesteadSigner{}, key)
	return tx
}

func getTxsAndSummary(n int, startNonce uint64, getGasLimit func(n int) uint64, getGasPrice func(n int) *big.Int, key *ecdsa.PrivateKey) ([]*types.InclusionListEntry, []*types.Transaction) {
	summary := make([]*types.InclusionListEntry, 0, n)
	txs := make([]*types.Transaction, 0, n)

	for i := 0; i < n; i++ {
		txs = append(txs, transaction(startNonce, getGasLimit(i), getGasPrice(i), key))
		summary = append(summary, &types.InclusionListEntry{Address: crypto.PubkeyToAddress(key.PublicKey), GasLimit: uint32(getGasLimit(i))})
		startNonce++
	}

	return summary, txs
}

func getGasLimitForTest(n int) uint64 {
	if n == 15 {
		return 1_000_000
	}
	return 100_000
}

func getGasPriceForTest(n int) *big.Int {
	// threshold = 1.125 * 1 Gwei
	if n == 0 {
		return big.NewInt(1_126_000_000)
	}
	if n == 17 {
		return big.NewInt(1_124_000_000)
	}
	return big.NewInt(1_125_000_000)
}

func getStateNonceForTest(n int) func(addr common.Address) uint64 {
	if n == 1 {
		return func(addr common.Address) uint64 {
			return 1
		}
	} else if n == 2 {
		return func(addr common.Address) uint64 {
			return 17
		}
	}

	return func(addr common.Address) uint64 {
		return 0
	}
}

func TestVerifyInclusionList(t *testing.T) {
	key, _ := crypto.GenerateKey()

	// Generate dummy summary and set of txs
	summary, txs := getTxsAndSummary(32, 0, getGasLimitForTest, getGasPriceForTest, key)

	// Modify a summary entry explicity for validating invalid
	// sender address check
	summary[16].Address = common.Address{}

	// Build a parent block such that the base fee stays the same
	// for the next block.
	parent := &types.Header{
		Number:   big.NewInt(0),
		GasLimit: 30_00_000,
		GasUsed:  15_00_000,
		BaseFee:  big.NewInt(1_000_000_000), // 1 GWei
	}

	testCases := []struct {
		name          string
		list          types.InclusionList
		parent        *types.Header
		config        *params.ChainConfig
		getStateNonce func(addr common.Address) uint64
		want          bool
		err           error
	}{
		{"empty inclusion list", types.InclusionList{Summary: summary[:0], Transactions: txs[:0]}, parent, params.TestChainConfig, getStateNonceForTest(0), true, nil},
		{"unqeual size of summary and transactions - 1", types.InclusionList{Summary: summary[:1], Transactions: txs[:0]}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrSizeMismatch},
		{"unqeual size of summary and transactions - 2", types.InclusionList{Summary: summary[:0], Transactions: txs[:1]}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrSizeMismatch},
		{"size exceeded", types.InclusionList{Summary: summary, Transactions: txs}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrSizeExceeded},
		{"gas limit exceeded", types.InclusionList{Summary: summary[:16], Transactions: txs[:16]}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrGasLimitExceeded},
		{"invalid sender address", types.InclusionList{Summary: summary[16:], Transactions: txs[16:]}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrSenderMismatch},
		{"invalid nonce - 1", types.InclusionList{Summary: summary[1:16], Transactions: txs[1:16]}, parent, params.TestChainConfig, getStateNonceForTest(0), false, ErrIncorrectNonce},
		{"invalid nonce - 2", types.InclusionList{Summary: summary[:16], Transactions: txs[:16]}, parent, params.TestChainConfig, getStateNonceForTest(1), false, ErrIncorrectNonce},
		{"less base fee", types.InclusionList{Summary: summary[17:], Transactions: txs[17:]}, parent, params.TestChainConfig, getStateNonceForTest(2), false, ErrInsufficientGasFeeCap},
		{"happy case", types.InclusionList{Summary: summary[:15], Transactions: txs[:15]}, parent, params.TestChainConfig, getStateNonceForTest(0), true, nil},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			res, err := verifyInclusionList(tc.list, parent, params.TestChainConfig, tc.getStateNonce)
			assert.Equal(t, res, tc.want, "result mismatch")
			assert.Equal(t, err, tc.err, "error mismatch")
		})
	}

}
