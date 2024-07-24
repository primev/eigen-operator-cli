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
	"github.com/primev/mev-commit/x/contracts/transactor"
	"github.com/primev/mev-commit/x/contracts/txmonitor"
	ks "github.com/primev/mev-commit/x/keysigner"
	"github.com/urfave/cli/v2"
)

type avsContractWithSesh struct {
	avs  *avs.Mevcommitavs
	sesh *avs.MevcommitavsTransactorSession
}

// TODO: re-eval if all these fields need to be stored
type Command struct {
	Logger           *slog.Logger
	OperatorConfig   *eigenclitypes.OperatorConfig
	KeystorePassword string
	signer           *ks.KeystoreSigner
	ethClient        *ethclient.Client

	MevCommitAVSAddress string
	avsContractWithSesh *avsContractWithSesh

	DelegationManagerAddress string
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

	c.Logger.Info("signer address", "address", c.signer.GetAddress().Hex())

	monitor := txmonitor.New(
		signer.GetAddress(),
		ethClient,
		txmonitor.NewEVMHelperWithLogger(ethClient.Client(), c.Logger),
		nil, // TOOD: re-eval if you need saver/store
		c.Logger.With("component", "txmonitor"),
		1, // TODO: re-eval max pending
	)
	transactor := transactor.NewTransactor(
		ethClient,
		monitor,
	)

	avsAddress := common.HexToAddress(c.MevCommitAVSAddress)
	mevCommitAVS, err := avs.NewMevcommitavs(avsAddress, transactor)
	if err != nil {
		return fmt.Errorf("failed to create mev-commit avs: %w", err)
	}

	c.Logger.Info("avs address", "address", avsAddress.Hex())

	tOpts, err := c.signer.GetAuth(chainID)
	if err != nil {
		c.Logger.Error("failed to get auth", "error", err)
		return err
	}
	// TODO: gas params would be changed here

	sesh := &avs.MevcommitavsTransactorSession{
		Contract:     &mevCommitAVS.MevcommitavsTransactor,
		TransactOpts: *tOpts,
	}

	c.avsContractWithSesh = &avsContractWithSesh{
		avs:  mevCommitAVS,
		sesh: sesh,
	}

	delegationManager, err := dm.NewContractDelegationManagerCaller(common.HexToAddress(""), nil) // TODO
	if err != nil {
		return fmt.Errorf("failed to create delegation manager: %w", err)
	}
	fmt.Println(delegationManager) // TODO

	return nil
}

func (c *Command) RegisterOperator(ctx *cli.Context) error {

	c.Logger.Info("Registering operator...")
	err := c.initialize(ctx)
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	operatorRegInfo, err := c.avsContractWithSesh.avs.GetOperatorRegInfo(
		&bind.CallOpts{Context: ctx.Context}, c.signer.GetAddress())
	if err != nil {
		return fmt.Errorf("failed to get operator reg info: %w", err)
	}
	if operatorRegInfo.Exists {
		return fmt.Errorf("signing operator already registered")
	}

	// TODO: also query EL's delegation manager

	operatorSig, err := c.generateOperatorSig()
	if err != nil {
		return fmt.Errorf("failed to generate operator sig: %w", err)
	}
	panic("next func is what errors. Seems onchain issue with recovered operator address?")

	tx, err := c.avsContractWithSesh.sesh.RegisterOperator(operatorSig)
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
	// TODO: Confirm we don't need to set gas params manually anymore?
	// See default gas limits in oracle's node.go

	// TODO: Determine if we want to support fee bump, cancelling, etc. Do some testing here..
	return nil
}

func (c *Command) generateOperatorSig() (avs.ISignatureUtilsSignatureWithSaltAndExpiry, error) {

	avsDirAddr, err := c.avsContractWithSesh.avs.AvsDirectory(&bind.CallOpts{})
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

	client, err := ethclient.Dial(ctx.String("eth-node-url"))
	if err != nil {
		return fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}
	c.Logger.Info("Client", "client", client)

	operatorAddress := ctx.String("operator-address")

	// Add your request deregistration logic here, e.g., sending a transaction to the Ethereum network
	c.Logger.Info("Operator deregistration requested", "address", operatorAddress)
	return nil
}

func (c *Command) DeregisterOperator(ctx *cli.Context) error {
	c.Logger.Info("Deregistering operator...")

	client, err := ethclient.Dial(ctx.String("eth-node-url"))
	if err != nil {
		return fmt.Errorf("failed to connect to Ethereum node: %w", err)
	}
	c.Logger.Info("Client", "client", client)

	operatorAddress := ctx.String("operator-address")

	// Add your deregistration logic here, e.g., sending a transaction to the Ethereum network
	c.Logger.Info("Operator deregistered", "address", operatorAddress)
	return nil
}
