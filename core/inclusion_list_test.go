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

func transaction(nonce uint64, gaslimit uint64, key *ecdsa.PrivateKey) *types.Transaction {
	return pricedTransaction(nonce, gaslimit, big.NewInt(30_000_000), key)
}

func pricedTransaction(nonce uint64, gaslimit uint64, gasprice *big.Int, key *ecdsa.PrivateKey) *types.Transaction {
	tx, _ := types.SignTx(types.NewTransaction(nonce, common.Address{}, big.NewInt(100), gaslimit, gasprice, nil), types.HomesteadSigner{}, key)
	return tx
}

func getTxsAndSummary(n int, startNonce uint64, getGasLimit func(n int) uint64, key *ecdsa.PrivateKey) ([]*types.InclusionListEntry, []*types.Transaction) {
	summary := make([]*types.InclusionListEntry, 0, n)
	txs := make([]*types.Transaction, 0, n)

	for i := 0; i < n; i++ {
		txs = append(txs, transaction(startNonce, getGasLimit(i), key))
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

func TestVerifyInclusionList(t *testing.T) {
	key, _ := crypto.GenerateKey()
	_ = key
	getStateNonce := func(addr common.Address) uint64 {
		return 0
	}
	getStateNonce2 := func(addr common.Address) uint64 {
		return 1
	}

	summary, txs := getTxsAndSummary(32, 0, getGasLimitForTest, key)
	summary[16].Address = common.Address{}

	parent := &types.Header{
		Number:   big.NewInt(0),
		GasLimit: 30_00_000,
		GasUsed:  15_00_000,
		BaseFee:  big.NewInt(15_000_000),
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
		{"empty inclusion list", types.InclusionList{Summary: summary[:0], Transactions: txs[:0]}, parent, params.TestChainConfig, getStateNonce, true, nil},
		{"unqeual size of summary and transactions - 1", types.InclusionList{Summary: summary[:1], Transactions: txs[:0]}, parent, params.TestChainConfig, getStateNonce, false, ErrSizeMismatch},
		{"unqeual size of summary and transactions - 2", types.InclusionList{Summary: summary[:0], Transactions: txs[:1]}, parent, params.TestChainConfig, getStateNonce, false, ErrSizeMismatch},
		{"size exceeded", types.InclusionList{Summary: summary, Transactions: txs}, parent, params.TestChainConfig, getStateNonce, false, ErrSizeExceeded},
		{"gas limit exceeded", types.InclusionList{Summary: summary[:16], Transactions: txs[:16]}, parent, params.TestChainConfig, getStateNonce, false, ErrGasLimitExceeded},
		{"invalid sender address", types.InclusionList{Summary: summary[16:], Transactions: txs[16:]}, parent, params.TestChainConfig, getStateNonce, false, ErrSenderMismatch},
		{"invalid nonce - 1", types.InclusionList{Summary: summary[1:16], Transactions: txs[1:16]}, parent, params.TestChainConfig, getStateNonce, false, ErrIncorrectNonce},
		{"invalid nonce - 2", types.InclusionList{Summary: summary[:16], Transactions: txs[:16]}, parent, params.TestChainConfig, getStateNonce2, false, ErrIncorrectNonce},
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
