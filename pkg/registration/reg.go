package registration

import (
	"context"
	"eigen-operator-cli/pkg/tx"
	"fmt"
	"math/big"
	"time"

	avsdir "github.com/Layr-Labs/eigensdk-go/contracts/bindings/AVSDirectory"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	avs "github.com/primev/mev-commit/contracts-abi/clients/MevCommitAVS"
	"github.com/urfave/cli/v2"
)

func (c *Command) RegisterOperator(ctx *cli.Context) error {
	c.Logger.Info("Registering operator...")
	err := c.initialize(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	operatorRegInfo, err := c.avsC.GetOperatorRegInfo(
		&bind.CallOpts{Context: ctx.Context}, c.account.Address)
	if err != nil {
		return fmt.Errorf("failed to get operator reg info: %w", err)
	}
	if operatorRegInfo.Exists {
		return fmt.Errorf("signing operator already registered")
	}

	isEigenOperator, err := c.dmC.IsOperator(&bind.CallOpts{}, c.account.Address)
	if err != nil {
		return fmt.Errorf("failed to check if operator is registered with eigen core: %w", err)
	}
	if !isEigenOperator {
		return fmt.Errorf("signer is not a registered operator with eigen core")
	}

	operatorSig, err := c.generateOperatorSig()
	if err != nil {
		return fmt.Errorf("failed to generate operator sig: %w", err)
	}

	submitTx := func(
		ctx context.Context,
		opts *bind.TransactOpts,
	) (*ethtypes.Transaction, error) {
		tx, err := c.avsT.RegisterOperator(c.tOpts, operatorSig)
		if err != nil {
			return nil, fmt.Errorf("failed to register operator: %w", err)
		}
		c.Logger.Info("RegisterOperator tx sent", "txHash", tx.Hash().Hex(), "nonce", tx.Nonce())
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
		errRevertReason := c.getRevertReason(ctx.Context, receipt)
		return fmt.Errorf("receipt status unsuccessful: %d, %w", receipt.Status, errRevertReason)
	}

	c.Logger.Info("Registration complete", "txHash", receipt.TxHash.Hex())
	return nil
}

func (c *Command) generateOperatorSig() (avs.ISignatureUtilsSignatureWithSaltAndExpiry, error) {

	avsDirAddr, err := c.avsC.AvsDirectory(&bind.CallOpts{})
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to get avs dir address: %w", err)
	}

	avsDir, err := avsdir.NewContractAVSDirectoryCaller(avsDirAddr, c.ethClient)
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to create avs dir: %w", err)
	}
	if avsDir == nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("avs dir is nil")
	}

	operatorAddr := c.account.Address
	salt := crypto.Keccak256Hash(operatorAddr.Bytes(), []byte(c.MevCommitAVSAddress))
	expiry := big.NewInt(time.Now().Add(time.Hour).Unix())
	digestHash, err := avsDir.CalculateOperatorAVSRegistrationDigestHash(&bind.CallOpts{},
		operatorAddr,
		common.HexToAddress(c.MevCommitAVSAddress),
		salt,
		expiry)
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to calculate digest hash: %w", err)
	}

	hashSig, err := c.keystore.SignHash(c.account, digestHash[:])
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to sign digest hash: %w", err)
	}

	// V is 0 or 1 from SignHash, but needs to be 27 or 28. See https://github.com/ethereum/go-ethereum/issues/19751
	if hashSig[64] < 27 {
		hashSig[64] += 27
	}

	return avs.ISignatureUtilsSignatureWithSaltAndExpiry{
		Signature: hashSig,
		Salt:      salt,
		Expiry:    expiry,
	}, nil
}

func (c *Command) getRevertReason(ctx context.Context, receipt *ethtypes.Receipt) error {
	tx, _, err := c.ethClient.TransactionByHash(ctx, receipt.TxHash)
	if err != nil {
		return fmt.Errorf("failed to get transaction: %w", err)
	}

	from, err := c.ethClient.TransactionSender(ctx, tx, receipt.BlockHash, receipt.TransactionIndex)
	if err != nil {
		return fmt.Errorf("failed to get transaction sender: %w", err)
	}

	msg := ethereum.CallMsg{
		From:     from,
		To:       tx.To(),
		Gas:      tx.Gas(),
		GasPrice: tx.GasPrice(),
		Value:    tx.Value(),
		Data:     tx.Data(),
	}

	_, err = c.ethClient.CallContract(ctx, msg, receipt.BlockNumber)
	return err
}
