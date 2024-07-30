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
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	avs "github.com/primev/mev-commit/contracts-abi/clients/MevCommitAVS"
	"github.com/urfave/cli/v2"
)

type Command struct {
	OperatorConfig      *eigenclitypes.OperatorConfig
	KeystorePassword    string
	MevCommitAVSAddress string
	BoostGasParams      bool
	Logger              *slog.Logger
	keystore            *keystore.KeyStore
	account             accounts.Account
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

	keystore := keystore.NewKeyStore(dir, keystore.LightScryptN, keystore.LightScryptP)
	ksAccounts := keystore.Accounts()

	var account accounts.Account
	if len(ksAccounts) == 0 {
		return fmt.Errorf("no accounts in dir: %s", dir)
	} else {
		found := false
		for _, acc := range ksAccounts {
			if acc.Address == common.HexToAddress(c.OperatorConfig.Operator.Address) {
				account = acc
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("account %s not found in keystore dir: %s", c.OperatorConfig.Operator.Address, dir)
		}
	}

	if err := keystore.Unlock(account, c.KeystorePassword); err != nil {
		return fmt.Errorf("failed to unlock account: %w", err)
	}

	c.keystore = keystore
	c.account = account

	c.Logger.Debug("signer address", "address", c.account.Address.Hex())

	pending, err := tx.PendingTransactionsExist(c.ethClient, ctx.Context, c.account.Address)
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

	tOpts, err := bind.NewKeyStoreTransactorWithChainID(keystore, account, chainID)
	if err != nil {
		c.Logger.Error("failed to get auth", "error", err)
		return err
	}
	tOpts.From = account.Address
	nonce, err := c.ethClient.PendingNonceAt(ctx.Context, account.Address)
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
	c.tOpts.GasLimit = 300000

	return nil
}
