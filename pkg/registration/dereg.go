package registration

import (
	"context"
	"eigen-operator-cli/pkg/tx"
	"fmt"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/urfave/cli/v2"
)

func (c *Command) DeregisterOperator(ctx *cli.Context) error {
	c.Logger.Info("Deregistering operator...")
	err := c.initialize(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	operatorRegInfo, err := c.avsC.GetOperatorRegInfo(
		&bind.CallOpts{Context: ctx.Context}, c.signer.GetAddress())
	if err != nil {
		return fmt.Errorf("failed to get operator reg info: %w", err)
	}
	if !operatorRegInfo.Exists {
		return fmt.Errorf("signing operator must be registered")
	}
	if !operatorRegInfo.DeregRequestHeight.Exists {
		return fmt.Errorf("signing operator must have requested deregistration")
	}

	operatorDeregPeriod, err := c.avsC.OperatorDeregPeriodBlocks(
		&bind.CallOpts{Context: ctx.Context})
	if err != nil {
		return fmt.Errorf("failed to get operator deregistration period: %w", err)
	}
	blockNum, err := c.ethClient.BlockNumber(ctx.Context)
	if err != nil {
		return fmt.Errorf("failed to get block number: %w", err)
	}
	blocksSinceDereg := blockNum - operatorRegInfo.DeregRequestHeight.BlockHeight.Uint64()
	if blocksSinceDereg <= operatorDeregPeriod.Uint64() {
		blocksRemaining := operatorDeregPeriod.Uint64() - blocksSinceDereg
		return fmt.Errorf("not enough blocks have passed since deregistration request. "+
			"Please wait %d more blocks", blocksRemaining)
	}

	submitTx := func(
		ctx context.Context,
		opts *bind.TransactOpts,
	) (*ethtypes.Transaction, error) {
		tx, err := c.avsT.DeregisterOperator(c.tOpts, c.signer.GetAddress())
		if err != nil {
			return nil, fmt.Errorf("failed to deregister operator: %w", err)
		}
		c.Logger.Info("DeregisterOperator tx sent", "txHash", tx.Hash().Hex(), "nonce", tx.Nonce())
		return tx, nil
	}

	var receipt *ethtypes.Receipt
	if c.BoostGasParams {
		receipt, err = tx.WaitMinedWithRetry(ctx.Context, c.tOpts, submitTx, c.ethClient, c.Logger)
		if err != nil {
			return fmt.Errorf("failed to wait for tx to be mined: %w", err)
		}
	} else {
		tx, err := submitTx(ctx.Context, c.tOpts)
		if err != nil {
			return fmt.Errorf("failed to submit tx: %w", err)
		}
		c.Logger.Info("waiting for tx to be mined", "txHash", tx.Hash().Hex(), "nonce", tx.Nonce())
		receipt, err = bind.WaitMined(ctx.Context, c.ethClient, tx)
		if err != nil {
			return fmt.Errorf("failed to wait for tx to be mined: %w", err)
		}
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		return fmt.Errorf("receipt status unsuccessful: %d", receipt.Status)
	}

	c.Logger.Info("DeregisterOperator complete", "txHash", receipt.TxHash.Hex())
	return nil
}
