package ante

import (
	"testing"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"
)

func TestSizeFeeCalculation(t *testing.T) {
	calculator := NewFeeCalculator(
		math.NewInt(1), // 1 token per byte base rate
		1000,           // threshold at 1000 bytes
		math.NewInt(2), // 2x multiplier for bytes above threshold
	)

	tests := []struct {
		name     string
		txSize   int64
		expected math.Int
	}{
		{
			name:     "Small transaction",
			txSize:   500,
			expected: math.NewInt(500), // 500 * 1 = 500
		},
		{
			name:     "Threshold size transaction",
			txSize:   1000,
			expected: math.NewInt(1000), // 1000 * 1 = 1000
		},
		{
			name:     "Large transaction",
			txSize:   1500,
			expected: math.NewInt(2000), // (1000 * 1) + (500 * 2) = 2000
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fee := calculator.calculateRequiredFee(tc.txSize)
			require.Equal(t, tc.expected, fee.AmountOf(sdk.DefaultBondDenom))
		})
	}
}
