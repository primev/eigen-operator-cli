package registration

import (
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"time"

	eigenclitypes "github.com/Layr-Labs/eigenlayer-cli/pkg/types"
	eigencliutils "github.com/Layr-Labs/eigenlayer-cli/pkg/utils"
	avsdir "github.com/Layr-Labs/eigensdk-go/contracts/bindings/AVSDirectory"
	dm "github.com/Layr-Labs/eigensdk-go/contracts/bindings/DelegationManager"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	avs "github.com/primev/mev-commit/contracts-abi/clients/MevCommitAVS"
	ks "github.com/primev/mev-commit/x/keysigner"
	"github.com/urfave/cli/v2"
)

type Command struct {
	OperatorConfig      *eigenclitypes.OperatorConfig
	KeystorePassword    string
	MevCommitAVSAddress string

	Logger    *slog.Logger
	signer    *ks.KeystoreSigner
	ethClient *ethclient.Client

	avsT  *avs.MevcommitavsTransactor
	avsC  *avs.MevcommitavsCaller
	dmC   *dm.ContractDelegationManagerCaller
	tOpts *bind.TransactOpts
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

	dmC, err := dm.NewContractDelegationManagerCaller(
		dmAddr, c.ethClient)
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

	// TODO: gas params would be changed here

	return nil
}

func (c *Command) RegisterOperator(ctx *cli.Context) error {

	c.Logger.Info("Registering operator...")
	err := c.initialize(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	operatorRegInfo, err := c.avsC.GetOperatorRegInfo(
		&bind.CallOpts{Context: ctx.Context}, c.signer.GetAddress())
	if err != nil {
		return fmt.Errorf("failed to get operator reg info: %w", err)
	}
	if operatorRegInfo.Exists {
		return fmt.Errorf("signing operator already registered")
	}

	isEigenOperator, err := c.dmC.IsOperator(&bind.CallOpts{}, c.signer.GetAddress())
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

	tx, err := c.avsT.RegisterOperator(c.tOpts, operatorSig)
	if err != nil {
		return fmt.Errorf("failed to register operator: %w", err)
	}

	c.Logger.Info("waiting for tx to be mined", "txHash", tx.Hash().Hex(), "nonce", tx.Nonce())
	rec, err := bind.WaitMined(ctx.Context, c.ethClient, tx)
	if err != nil {
		return fmt.Errorf("failed to wait for tx to be mined: %w", err)
	} else if rec.Status != ethtypes.ReceiptStatusSuccessful {
		return fmt.Errorf("receipt status unsuccessful: %d", rec.Status)
	}

	c.Logger.Info("Registration complete", "txHash", tx.Hash().Hex(), "nonce", tx.Nonce())

	// TODO: Confirm we don't need to set gas params manually anymore?
	// See default gas limits in oracle's node.go

	// TODO: Determine if we want to support fee bump, cancelling, etc. Do some testing here..
	return nil
}

func (c *Command) generateOperatorSig() (avs.ISignatureUtilsSignatureWithSaltAndExpiry, error) {

	avsDirAddr, err := c.avsC.AvsDirectory(&bind.CallOpts{})
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to get avs dir address: %w", err)
	}

	// TODO: confirm most recent bindings are backwards compatible with avs dir on holesky
	avsDir, err := avsdir.NewContractAVSDirectoryCaller(avsDirAddr, c.ethClient)
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to create avs dir: %w", err)
	}
	if avsDir == nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("avs dir is nil")
	}

	operatorAddr := c.signer.GetAddress()
	salt := crypto.Keccak256Hash(operatorAddr.Bytes())
	expiry := big.NewInt(time.Now().Add(time.Hour).Unix())
	digestHash, err := avsDir.CalculateOperatorAVSRegistrationDigestHash(&bind.CallOpts{},
		operatorAddr,
		common.HexToAddress(c.MevCommitAVSAddress),
		salt,
		expiry)
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to calculate digest hash: %w", err)
	}

	hashSig, err := c.signer.SignHash(digestHash[:])
	if err != nil {
		return avs.ISignatureUtilsSignatureWithSaltAndExpiry{}, fmt.Errorf("failed to sign digest hash: %w", err)
	}

	return avs.ISignatureUtilsSignatureWithSaltAndExpiry{
		Signature: hashSig,
		Salt:      salt,
		Expiry:    expiry,
	}, nil
}

func (c *Command) RequestOperatorDeregistration(ctx *cli.Context) error {
	c.Logger.Info("Requesting operator deregistration...")
	panic("TODO")
}

func (c *Command) DeregisterOperator(ctx *cli.Context) error {
	c.Logger.Info("Deregistering operator...")
	panic("TODO")
}
