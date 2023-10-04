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

// VerifyInclusionList verifies the properties of the inclusion list and the
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
