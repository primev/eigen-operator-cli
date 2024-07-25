package tx

import (
	"context"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	client "github.com/ethereum/go-ethereum/ethclient"
)

type EthClient interface {
	bind.DeployBackend
	PendingNonceAt(ctx context.Context, account common.Address) (uint64, error)
	NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error)
	SuggestGasTipCap(ctx context.Context) (*big.Int, error)
	SuggestGasPrice(ctx context.Context) (*big.Int, error)
}

// Primary target for EthClient is go-ethereum/ethclient/Client
var _ EthClient = (*client.Client)(nil)

func PendingTransactionsExist(
	client EthClient, ctx context.Context, address common.Address) (bool, error) {

	currentNonce, err := client.PendingNonceAt(ctx, address)
	if err != nil {
		return false, fmt.Errorf("failed to get current pending nonce: %w", err)
	}
	latestNonce, err := client.NonceAt(ctx, address, nil)
	if err != nil {
		return false, fmt.Errorf("failed to get latest nonce: %w", err)
	}
	return currentNonce > latestNonce, nil
}

func SuggestGasTipCapAndPrice(ctx context.Context, client EthClient) (
	gasTip *big.Int, gasPrice *big.Int, err error) {

	// Returns priority fee per gas
	gasTip, err = client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gas tip cap: %w", err)
	}
	// Returns priority fee per gas + base fee per gas
	gasPrice, err = client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get gas price: %w", err)
	}
	return gasTip, gasPrice, nil
}

// Boosts the gas tip and base fee by just above 10% of highest recent suggestion from client.
func BoostTipForTransactOpts(ctx context.Context, opts *bind.TransactOpts, client EthClient, logger *slog.Logger) error {

	if opts.GasTipCap == nil || opts.GasFeeCap == nil {
		return fmt.Errorf("gas tip cap and gas fee cap must be set")
	}

	logger.Debug(
		"gas params for tx that were not included",
		"gas_tip", opts.GasTipCap.String(),
		"gas_fee_cap", opts.GasFeeCap.String(),
		"base_fee", new(big.Int).Sub(opts.GasFeeCap, opts.GasTipCap).String(),
	)

	newGasTip, newFeeCap, err := SuggestGasTipCapAndPrice(ctx, client)
	if err != nil {
		return fmt.Errorf("failed to suggest gas tip cap and price: %w", err)
	}

	newBaseFee := new(big.Int).Sub(newFeeCap, newGasTip)
	if newBaseFee.Cmp(big.NewInt(0)) == -1 {
		return fmt.Errorf("new base fee cannot be negative: %s", newBaseFee.String())
	}
	prevBaseFee := new(big.Int).Sub(opts.GasFeeCap, opts.GasTipCap)
	if prevBaseFee.Cmp(big.NewInt(0)) == -1 {
		return fmt.Errorf("base fee cannot be negative: %s", prevBaseFee.String())
	}

	var maxBaseFee *big.Int
	if newBaseFee.Cmp(prevBaseFee) == 1 {
		maxBaseFee = newBaseFee
	} else {
		maxBaseFee = prevBaseFee
	}

	var maxGasTip *big.Int
	if newGasTip.Cmp(opts.GasTipCap) == 1 {
		maxGasTip = newGasTip
	} else {
		maxGasTip = opts.GasTipCap
	}

	boostedTip := addTenPercentTo(maxGasTip)
	boostedTip = boostedTip.Add(boostedTip, big.NewInt(1))

	boostedBaseFee := addTenPercentTo(maxBaseFee)

	opts.GasTipCap = boostedTip
	opts.GasFeeCap = new(big.Int).Add(boostedBaseFee, boostedTip)

	logger.Info(
		"boosting gas tip and base fee by 10 percent for faster tx inclusion",
		"boosted_gas_tip_cap", opts.GasTipCap.String(),
		"boosted_gas_fee_cap", opts.GasFeeCap.String(),
		"boosted_base_fee", boostedBaseFee.String(),
	)

	return nil
}

func addTenPercentTo(value *big.Int) *big.Int {
	return new(big.Int).Add(value, new(big.Int).Div(value, big.NewInt(10)))
}

type TxSubmitFunc func(
	ctx context.Context,
	opts *bind.TransactOpts,
) (
	tx *types.Transaction,
	err error,
)

func WaitMinedWithRetry(ctx context.Context, opts *bind.TransactOpts, submitTx TxSubmitFunc,
	client EthClient, logger *slog.Logger) (*types.Receipt, error) {

	const maxRetries = 10
	var err error
	var tx *types.Transaction

	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			logger.Info("transaction not included within 60 seconds, boosting gas tip by 10%", "attempt", attempt)
			if err := BoostTipForTransactOpts(ctx, opts, client, logger); err != nil {
				return nil, fmt.Errorf("failed to boost gas tip for attempt %d: %w", attempt, err)
			}
		}

		tx, err = submitTx(ctx, opts)
		if err != nil {
			if strings.Contains(err.Error(), "replacement transaction underpriced") || strings.Contains(err.Error(), "already known") {
				logger.Debug("tx submission failed", "attempt", attempt, "error", err)
				continue
			}
			return nil, fmt.Errorf("tx submission failed on attempt %d: %w", attempt, err)
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		receiptChan := make(chan *types.Receipt)
		errChan := make(chan error)

		go func() {
			receipt, err := bind.WaitMined(timeoutCtx, client, tx)
			if err != nil {
				errChan <- err
				return
			}
			receiptChan <- receipt
		}()

		select {
		case receipt := <-receiptChan:
			cancel()
			return receipt, nil
		case err := <-errChan:
			cancel()
			return nil, err
		case <-timeoutCtx.Done():
			cancel()
			if attempt == maxRetries-1 {
				return nil, fmt.Errorf("tx not included after %d attempts", maxRetries)
			}
			// Continue with boosted tip
		}
	}
	return nil, fmt.Errorf("unexpected error: control flow should not reach end of WaitMinedWithRetry")
}
