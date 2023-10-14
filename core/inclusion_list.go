package core

import (
	"errors"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/consensus/misc/eip1559"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
)

// IL constants taken from specs here: https://github.com/potuz/consensus-specs/blob/a6c55576de059a1b2cae69848dee827f6e26e72d/specs/_features/epbs/beacon-chain.md#execution
const (
	MAX_TRANSACTIONS_PER_INCLUSION_LIST = 16
	MAX_GAS_PER_INCLUSION_LIST          = 2_097_152 // 2^21
)

// verifyInclusionList verifies the properties of the inclusion list and the
// transactions in it based on a `parent` block.
func verifyInclusionList(list types.InclusionList, parent *types.Header, config *params.ChainConfig, getStateNonce func(addr common.Address) uint64) (bool, error) {
	if len(list.Summary) != len(list.Transactions) {
		return false, errors.New("IL summary and transactions length mismatch")
	}

	if len(list.Summary) > MAX_TRANSACTIONS_PER_INCLUSION_LIST {
		log.Debug("IL verification failed: exceeds maximum number of transactions", "len", len(list.Summary), "max", MAX_TRANSACTIONS_PER_INCLUSION_LIST)
		return false, errors.New("IL exceeds maximum number of transactions")
	}

	// As IL will be included in the next block, calculate the current block's base fee.
	// As the current block's payload isn't revealed yet (due to ePBS), calculate
	// it from parent block.
	currentBaseFee := eip1559.CalcBaseFee(config, parent)

	// 1.125 * currentBaseFee
	gasFeeThreshold := new(big.Float).Mul(new(big.Float).SetFloat64(1.125), new(big.Float).SetInt(currentBaseFee))

	// Prepare the signer object
	signer := types.LatestSigner(config)

	// Create a nonce cache
	nonceCache := make(map[common.Address]uint64)

	// Track total gas limit
	gasLimit := uint64(0)

	// Verify if the summary and transactions match. Also check if the txs
	// have at least 12.5% higher `maxFeePerGas` than parent block's base fee.
	for i, summary := range list.Summary {
		tx := list.Transactions[i]

		// Don't allow BlobTxs
		if tx.Type() == types.BlobTxType {
			return false, errors.New("received blob tx in IL")
		}

		// Verify gas limit
		gasLimit += tx.Gas()

		if gasLimit > MAX_GAS_PER_INCLUSION_LIST {
			log.Debug("IL verification failed: gas limit exceeds maximum allowed", "gaslimit", gasLimit, "max", MAX_GAS_PER_INCLUSION_LIST)
			return false, errors.New("IL gas limit exceeds maximum allowed")
		}

		// Verify sender
		from, err := types.Sender(signer, tx)
		if err != nil {
			log.Debug("IL verification failed: unable to get sender from transaction", "err", err)
			return false, errors.New("invalid tx in IL")
		}
		if summary.Address != from {
			log.Debug("IL verification failed: summary and transaction address mismatch", "summary", summary.Address, "tx", from)
			return false, errors.New("summary and transaction address mismatch in IL")
		}

		// Verify nonce from state
		nonce := getStateNonce(from)
		if cacheNonce, ok := nonceCache[from]; ok {
			nonce = cacheNonce
		}

		if tx.Nonce() == nonce+1 {
			nonceCache[from] = tx.Nonce()
		} else {
			log.Debug("IL verification failed: incorrect nonce", "state nonce", nonce, "tx nonce", tx.Nonce())
			return false, errors.New("incorrect nonce in IL")
		}

		// Verify gas fee: tx.GasFeeCap > 1.125 * gasFeeThreshold
		if new(big.Float).SetInt(tx.GasFeeCap()).Cmp(gasFeeThreshold) == -1 {
			log.Debug("IL verification failed: insufficient gas fee cap", "gasFeeCap", tx.GasFeeCap(), "threshold", gasFeeThreshold)
			return false, errors.New("insufficient gas fee cap in IL")
		}
	}

	log.Debug("IL verified successfully", "len", len(list.Summary), "gas", gasLimit)

	return true, nil
}

// verifyInclusionListInBlock verifies if a block satisfies the inclusion list summary
// or not. Note that this function doesn't validate the state transition. It can be
// considered as a filter before sending the block to state transition. This function
// assumes that basic validations are already done. It only checks the following things:
//
//  1. If the indices in the exclusion list pointing to the parent block transactions
//     are present in the summary or not.
//  2. If the remaining summary entries are satisfied by the first `k` transactions
//     of the current block.
func verifyInclusionListInBlock(list types.InclusionList, exclusionList []uint64, parentTxs, currentTxs types.Transactions, config *params.ChainConfig) (bool, error) {
	// We assume that summary isn't ordered
	// Prepare a map of summary entries: address -> []{gas limit}.
	summaries := make(map[common.Address][]uint32)
	for _, summary := range list.Summary {
		if _, ok := summaries[summary.Address]; !ok {
			summaries[summary.Address] = make([]uint32, 0)
		}
		summaries[summary.Address] = append(summaries[summary.Address], summary.GasLimit)
	}

	// Prepare a map for txs in the IL
	ilTxs := make(map[common.Hash]*types.Transaction)
	for _, tx := range list.Transactions {
		ilTxs[tx.Hash()] = tx
	}

	// Prepare the signer object
	signer := types.LatestSigner(config)

	exclusions := 0
	for _, index := range exclusionList {
		tx := parentTxs[index]

		// Verify sender
		from, err := types.Sender(signer, tx)
		if err != nil {
			return false, errors.New("invalid tx in parent block")
		}

		if entries, ok := summaries[from]; !ok || len(entries) == 0 {
			return false, errors.New("missing summary entry")
		}

		summaries[from] = summaries[from][1:]
		exclusions++
	}

	index := 0
	for {
		if exclusions < len(list.Summary) {
			break
		}

		tx := currentTxs[index]

		// Verify sender
		from, err := types.Sender(signer, tx)
		if err != nil {
			return false, errors.New("invalid tx in current block")
		}

		if entries, ok := summaries[from]; !ok || len(entries) == 0 {
			return false, errors.New("missing IL in current block")
		}

		if summaries[from][0] > uint32(tx.Gas()) {
			return false, errors.New("invalid gas limit")
		}
		summaries[from] = summaries[from][1:]
		exclusions++

		// Verify hash
		if _, ok := ilTxs[tx.Hash()]; !ok {
			return false, errors.New("missing IL in current block")
		}

		index++
	}

	return true, nil
}
