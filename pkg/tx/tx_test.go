package tx_test

import (
	"context"
	"eigen-operator-cli/pkg/tx"
	"log/slog"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"gotest.tools/assert"
)

func NewMockEthClient(
	suggestGasTipCap func() (*big.Int, error),
	suggestGasPrice func() (*big.Int, error)) *MockEthClient {
	return &MockEthClient{
		suggestGasTipCap: suggestGasTipCap,
		suggestGasPrice:  suggestGasPrice,
	}
}

type MockEthClient struct {
	suggestGasTipCap func() (*big.Int, error)
	suggestGasPrice  func() (*big.Int, error)
}

func (m *MockEthClient) TransactionReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	return nil, nil
}

func (m *MockEthClient) CodeAt(ctx context.Context, account common.Address, blockNumber *big.Int) ([]byte, error) {
	return nil, nil
}

func (m *MockEthClient) PendingNonceAt(ctx context.Context, account common.Address) (uint64, error) {
	return 0, nil
}

func (m *MockEthClient) NonceAt(ctx context.Context, account common.Address, blockNumber *big.Int) (uint64, error) {
	return 0, nil
}

func (m *MockEthClient) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return m.suggestGasTipCap()
}

func (m *MockEthClient) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	return m.suggestGasPrice()
}

func TestBoostTipForTransactOpts(t *testing.T) {

	type gasParams struct {
		GasTipCap *big.Int
		GasFeeCap *big.Int
	}

	testCases := []struct {
		name              string
		suggestGasTipCap  func() (*big.Int, error)
		suggestGasPrice   func() (*big.Int, error)
		gasParams         gasParams
		expectedGasParams gasParams
		errExpectedOutput string
	}{
		{
			name:              "error, empty gas params",
			gasParams:         gasParams{},
			expectedGasParams: gasParams{},
			errExpectedOutput: "gas tip cap and gas fee cap must be set",
		},
		{
			name: "error, empty gas tip cap",
			gasParams: gasParams{
				GasFeeCap: big.NewInt(1000000000000000000),
			},
			expectedGasParams: gasParams{
				GasFeeCap: big.NewInt(1000000000000000000),
			},
			errExpectedOutput: "gas tip cap and gas fee cap must be set",
		},
		{
			name: "error, empty gas fee cap",
			gasParams: gasParams{
				GasTipCap: big.NewInt(1000000000000000000),
			},
			expectedGasParams: gasParams{
				GasTipCap: big.NewInt(1000000000000000000),
			},
			errExpectedOutput: "gas tip cap and gas fee cap must be set",
		},
		{
			name: "boosted",
			suggestGasTipCap: func() (*big.Int, error) {
				return big.NewInt(1000000000000000000), nil
			},
			suggestGasPrice: func() (*big.Int, error) {
				return big.NewInt(1000000000000000000), nil
			},
			gasParams: gasParams{
				GasTipCap: big.NewInt(1000000000000000000),
				GasFeeCap: big.NewInt(1000000000000000000),
			},
			expectedGasParams: gasParams{
				GasTipCap: big.NewInt(1100000000000000001),
				GasFeeCap: big.NewInt(1100000000000000001),
			},
		},
		{
			name: "boosted, suggestions increased",
			suggestGasTipCap: func() (*big.Int, error) {
				return big.NewInt(600), nil
			},
			suggestGasPrice: func() (*big.Int, error) {
				return big.NewInt(1000), nil
			},
			gasParams: gasParams{
				GasTipCap: big.NewInt(500),
				GasFeeCap: big.NewInt(900),
			},
			expectedGasParams: gasParams{
				GasTipCap: big.NewInt(661),  // 1.1 * 600 + 1
				GasFeeCap: big.NewInt(1101), // 1.1 * 900 + 1
			},
		},
		{
			name: "boosted, suggestions decreased",
			suggestGasTipCap: func() (*big.Int, error) {
				return big.NewInt(100), nil
			},
			suggestGasPrice: func() (*big.Int, error) {
				return big.NewInt(150), nil
			},
			gasParams: gasParams{
				GasTipCap: big.NewInt(250),
				GasFeeCap: big.NewInt(300),
			},
			expectedGasParams: gasParams{
				GasTipCap: big.NewInt(276), // 1.1 * 250 + 1
				GasFeeCap: big.NewInt(331), // 1.1 * 300 + 1
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			mockEthClient := NewMockEthClient(tc.suggestGasTipCap, tc.suggestGasPrice)
			opts := &bind.TransactOpts{}
			opts.GasFeeCap = tc.gasParams.GasFeeCap
			opts.GasTipCap = tc.gasParams.GasTipCap
			errOutput := tx.BoostTipForTransactOpts(
				context.Background(), opts, mockEthClient, slog.Default(),
			)
			outputParams := gasParams{
				GasTipCap: opts.GasTipCap,
				GasFeeCap: opts.GasFeeCap,
			}
			if tc.errExpectedOutput != "" {
				assert.Error(t, errOutput, tc.errExpectedOutput)
			} else {
				assert.NilError(t, errOutput)
			}
			if tc.expectedGasParams.GasFeeCap != nil {
				assert.Equal(t, tc.expectedGasParams.GasFeeCap.Uint64(), outputParams.GasFeeCap.Uint64())
			}
			if tc.expectedGasParams.GasTipCap != nil {
				assert.Equal(t, tc.expectedGasParams.GasTipCap.Uint64(), outputParams.GasTipCap.Uint64())
			}
		})
	}
}
