package registration

import (
	"eigen-operator-cli/pkg/tx"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"

	eigenclitypes "github.com/Layr-Labs/eigenlayer-cli/pkg/types"
	eigencliutils "github.com/Layr-Labs/eigenlayer-cli/pkg/utils"
	dm "github.com/Layr-Labs/eigensdk-go/contracts/bindings/DelegationManager"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	avs "github.com/primev/mev-commit/contracts-abi/clients/MevCommitAVS"
	ks "github.com/primev/mev-commit/x/keysigner"
	"github.com/urfave/cli/v2"
)

type Command struct {
	OperatorConfig      *eigenclitypes.OperatorConfig
	KeystorePassword    string
	MevCommitAVSAddress string
	BoostGasParams      bool
	Logger              *slog.Logger
	signer              *ks.KeystoreSigner
	ethClient           *ethclient.Client
	avsT                *avs.MevcommitavsTransactor
	avsC                *avs.MevcommitavsCaller
	dmC                 *dm.ContractDelegationManagerCaller
	tOpts               *bind.TransactOpts
}

func (c *Command) initialize(ctx *cli.Context) error {
	ethClient, err := ethclient.Dial(c.OperatorConfig.EthRPCUrl)
	if err != nil {
		return fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}
	c.ethClient = ethClient

	chainID, err := ethClient.ChainID(ctx.Context)
	if err != nil {
		c.Logger.Error("failed to get chain ID", "error", err)
		return err
	}
	if chainID.Cmp(&c.OperatorConfig.ChainId) != 0 {
		return fmt.Errorf("chain ID from rpc url doesn't match operator config: %s != %s",
			chainID.String(), c.OperatorConfig.ChainId.String())
	}
	c.Logger.Info("Chain ID", "chainID", chainID)

	_, err = os.Stat(c.OperatorConfig.PrivateKeyStorePath)
	if err != nil {
		return fmt.Errorf("no keystore file found at path: %s", c.OperatorConfig.PrivateKeyStorePath)
	}

	if c.KeystorePassword == "" {
		prompter := eigencliutils.NewPrompter()
		keystorePwd, err := prompter.InputHiddenString(
			fmt.Sprintf("Enter password to decrypt ecdsa keystore for %s:", c.OperatorConfig.PrivateKeyStorePath), "",
			func(string) error {
				return nil
			},
		)
		if err != nil {
			return fmt.Errorf("failed to read keystore password: %w", err)
		}
		c.KeystorePassword = keystorePwd
	}

	dir := filepath.Dir(c.OperatorConfig.PrivateKeyStorePath)
	signer, err := ks.NewKeystoreSigner(dir, c.KeystorePassword)
	if err != nil {
		return fmt.Errorf("failed to create keystore signer: %w", err)
	}
	c.signer = signer
	c.Logger.Debug("signer address", "address", c.signer.GetAddress().Hex())

	pending, err := tx.PendingTransactionsExist(c.ethClient, ctx.Context, c.signer.GetAddress())
	if err != nil {
		return fmt.Errorf("failed to check for pending transactions: %w", err)
	}
	if pending {
		return fmt.Errorf("pending transactions found for signing operator account. " +
			"Please cancel or wait for them to be mined before proceeding")
	}

	avsAddress := common.HexToAddress(c.MevCommitAVSAddress)
	c.Logger.Debug("avs address", "address", avsAddress.Hex())

	avsT, err := avs.NewMevcommitavsTransactor(avsAddress, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create avs transactor: %w", err)
	}
	c.avsT = avsT

	avsC, err := avs.NewMevcommitavsCaller(avsAddress, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create avs caller: %w", err)
	}
	c.avsC = avsC

	dmAddr := common.HexToAddress(c.OperatorConfig.ELDelegationManagerAddress)
	c.Logger.Debug("delegation manager address", "address", dmAddr.Hex())

	dmC, err := dm.NewContractDelegationManagerCaller(dmAddr, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to create delegation manager: %w", err)
	}
	c.dmC = dmC

	tOpts, err := c.signer.GetAuth(chainID)
	if err != nil {
		c.Logger.Error("failed to get auth", "error", err)
		return err
	}
	tOpts.From = c.signer.GetAddress()
	nonce, err := c.ethClient.PendingNonceAt(ctx.Context, c.signer.GetAddress())
	if err != nil {
		return fmt.Errorf("failed to get pending nonce: %w", err)
	}
	tOpts.Nonce = big.NewInt(int64(nonce))
	c.tOpts = tOpts

	gasTip, gasPrice, err := tx.SuggestGasTipCapAndPrice(ctx.Context, c.ethClient)
	if err != nil {
		return fmt.Errorf("failed to suggest gas tip cap and price: %w", err)
	}
	c.tOpts.GasFeeCap = gasPrice
	c.tOpts.GasTipCap = gasTip
	c.tOpts.GasLimit = 200000 // TODO: Test this value

	return nil
}
