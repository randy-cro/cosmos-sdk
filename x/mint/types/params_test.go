package types

import (
	"math"
	"testing"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"
)

func TestDefaultParams_ValidateOK(t *testing.T) {
	t.Parallel()
	require.NoError(t, DefaultParams().Validate())
}

func TestNewParams_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		params      Params
		wantErr     bool
		errContains string
	}{
		{
			name: "valid typical config",
			params: NewParams(
				"uatom",
				sdkmath.LegacyMustNewDecFromStr("0.13"),
				sdkmath.LegacyMustNewDecFromStr("0.20"),
				sdkmath.LegacyMustNewDecFromStr("0.07"),
				sdkmath.LegacyMustNewDecFromStr("0.67"),
				uint64(60*60*8766/5),
				1,
				sdkmath.LegacyZeroDec(),
			),
			wantErr:     false,
			errContains: "",
		},
		{
			name: "max < min inflation",
			params: NewParams(
				"uatom",
				sdkmath.LegacyMustNewDecFromStr("0.10"),
				sdkmath.LegacyMustNewDecFromStr("0.05"),
				sdkmath.LegacyMustNewDecFromStr("0.06"),
				sdkmath.LegacyMustNewDecFromStr("0.67"),
				1,
				1,
				sdkmath.LegacyZeroDec(),
			),
			wantErr:     true,
			errContains: "must be greater than or equal to min inflation",
		},
		{
			name: "invalid denom",
			params: NewParams(
				"",
				sdkmath.LegacyMustNewDecFromStr("0.10"),
				sdkmath.LegacyMustNewDecFromStr("0.20"),
				sdkmath.LegacyMustNewDecFromStr("0.07"),
				sdkmath.LegacyMustNewDecFromStr("0.67"),
				1,
				1,
				sdkmath.LegacyZeroDec(),
			),
			wantErr:     true,
			errContains: "",
		},
		{
			name: "goal bonded > 1",
			params: NewParams(
				"uatom",
				sdkmath.LegacyMustNewDecFromStr("0.10"),
				sdkmath.LegacyMustNewDecFromStr("0.20"),
				sdkmath.LegacyMustNewDecFromStr("0.07"),
				sdkmath.LegacyMustNewDecFromStr("1.01"),
				1,
				1,
				sdkmath.LegacyZeroDec(),
			),
			wantErr:     true,
			errContains: "",
		},
		{
			name: "blocks per year zero",
			params: NewParams(
				"uatom",
				sdkmath.LegacyMustNewDecFromStr("0.10"),
				sdkmath.LegacyMustNewDecFromStr("0.20"),
				sdkmath.LegacyMustNewDecFromStr("0.07"),
				sdkmath.LegacyMustNewDecFromStr("0.67"),
				0,
				1,
				sdkmath.LegacyZeroDec(),
			),
			wantErr:     true,
			errContains: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.params.Validate()
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateMintDenom(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		denom       string
		wantErr     bool
		errContains string
	}{
		{name: "blank", denom: "", wantErr: true, errContains: "cannot be blank"},
		{name: "spaces", denom: "   ", wantErr: true, errContains: "cannot be blank"},
		{name: "valid lower", denom: "uatom", wantErr: false, errContains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateMintDenom(tt.denom)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateInflationRateChange(t *testing.T) {
	t.Parallel()

	var nilDec sdkmath.LegacyDec // zero value => IsNil() == true

	tests := []struct {
		name        string
		val         sdkmath.LegacyDec
		wantErr     bool
		errContains string
	}{
		{name: "nil", val: nilDec, wantErr: true, errContains: "cannot be nil"},
		{name: "negative", val: sdkmath.LegacyMustNewDecFromStr("-0.01"), wantErr: true, errContains: "cannot be negative"},
		{name: "too large > 1", val: sdkmath.LegacyMustNewDecFromStr("1.01"), wantErr: true, errContains: "too large"},
		{name: "zero ok", val: sdkmath.LegacyMustNewDecFromStr("0"), wantErr: false, errContains: ""},
		{name: "one ok", val: sdkmath.LegacyMustNewDecFromStr("1.0"), wantErr: false, errContains: ""},
		{name: "mid ok", val: sdkmath.LegacyMustNewDecFromStr("0.13"), wantErr: false, errContains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateInflationRateChange(tt.val)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateInflationMax(t *testing.T) {
	t.Parallel()

	var nilDec sdkmath.LegacyDec

	tests := []struct {
		name        string
		val         sdkmath.LegacyDec
		wantErr     bool
		errContains string
	}{
		{name: "nil", val: nilDec, wantErr: true, errContains: "cannot be nil"},
		{name: "negative", val: sdkmath.LegacyMustNewDecFromStr("-0.01"), wantErr: true, errContains: "cannot be negative"},
		{name: "too large > 1", val: sdkmath.LegacyMustNewDecFromStr("1.1"), wantErr: true, errContains: "too large"},
		{name: "zero ok", val: sdkmath.LegacyMustNewDecFromStr("0"), wantErr: false, errContains: ""},
		{name: "one ok", val: sdkmath.LegacyMustNewDecFromStr("1"), wantErr: false, errContains: ""},
		{name: "typical ok", val: sdkmath.LegacyMustNewDecFromStr("0.20"), wantErr: false, errContains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateInflationMax(tt.val)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateInflationMin(t *testing.T) {
	t.Parallel()

	var nilDec sdkmath.LegacyDec

	tests := []struct {
		name        string
		val         sdkmath.LegacyDec
		wantErr     bool
		errContains string
	}{
		{name: "nil", val: nilDec, wantErr: true, errContains: "cannot be nil"},
		{name: "negative", val: sdkmath.LegacyMustNewDecFromStr("-0.01"), wantErr: true, errContains: "cannot be negative"},
		{name: "too large > 1", val: sdkmath.LegacyMustNewDecFromStr("1.1"), wantErr: true, errContains: "too large"},
		{name: "zero ok", val: sdkmath.LegacyMustNewDecFromStr("0"), wantErr: false, errContains: ""},
		{name: "one ok", val: sdkmath.LegacyMustNewDecFromStr("1"), wantErr: false, errContains: ""},
		{name: "typical ok", val: sdkmath.LegacyMustNewDecFromStr("0.07"), wantErr: false, errContains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateInflationMin(tt.val)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateGoalBonded(t *testing.T) {
	t.Parallel()

	var nilDec sdkmath.LegacyDec

	tests := []struct {
		name        string
		val         sdkmath.LegacyDec
		wantErr     bool
		errContains string
	}{
		{name: "nil", val: nilDec, wantErr: true, errContains: "cannot be nil"},
		{name: "negative", val: sdkmath.LegacyMustNewDecFromStr("-0.01"), wantErr: true, errContains: "must be positive"},
		{name: "zero", val: sdkmath.LegacyMustNewDecFromStr("0"), wantErr: true, errContains: "must be positive"},
		{name: "too large > 1", val: sdkmath.LegacyMustNewDecFromStr("1.0000000001"), wantErr: true, errContains: "too large"},
		{name: "exactly one ok", val: sdkmath.LegacyMustNewDecFromStr("1.0"), wantErr: false, errContains: ""},
		{name: "typical ok", val: sdkmath.LegacyMustNewDecFromStr("0.67"), wantErr: false, errContains: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateGoalBonded(tt.val)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestValidateBlocksPerYear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		val         uint64
		wantErr     bool
		errContains string
	}{
		{name: "zero", val: 0, wantErr: true, errContains: "must be positive"},
		{name: "one ok", val: 1, wantErr: false, errContains: ""},
		{name: "maxInt64 ok", val: uint64(math.MaxInt64), wantErr: false, errContains: ""},
		{name: "maxInt64+1 too large", val: uint64(math.MaxInt64) + 1, wantErr: true, errContains: "too large"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := validateBlocksPerYear(tt.val)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.ErrorContains(t, err, tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParams_ValidateDecayStartHeight(t *testing.T) {
	tests := []struct {
		name    string
		height  uint64
		wantErr bool
	}{
		{
			name:    "valid positive height",
			height:  1,
			wantErr: false,
		},
		{
			name:    "valid large height",
			height:  1000000,
			wantErr: false,
		},
		{
			name:    "invalid zero height",
			height:  0,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := DefaultParams()
			params.DecayStartHeight = tt.height

			err := params.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "decay start height must be positive")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestParams_ValidateDecayRate(t *testing.T) {
	tests := []struct {
		name    string
		rate    sdkmath.LegacyDec
		wantErr bool
	}{
		{
			name:    "valid zero rate (disabled)",
			rate:    sdkmath.LegacyZeroDec(),
			wantErr: false,
		},
		{
			name:    "valid small rate",
			rate:    sdkmath.LegacyNewDecWithPrec(1, 2), // 0.01 = 1%
			wantErr: false,
		},
		{
			name:    "valid medium rate",
			rate:    sdkmath.LegacyNewDecWithPrec(65, 3), // 0.065 = 6.5%
			wantErr: false,
		},
		{
			name:    "valid one (100%)",
			rate:    sdkmath.LegacyOneDec(),
			wantErr: false,
		},
		{
			name:    "invalid negative rate",
			rate:    sdkmath.LegacyNewDecWithPrec(-1, 2),
			wantErr: true,
		},
		{
			name:    "invalid rate greater than one",
			rate:    sdkmath.LegacyNewDecWithPrec(101, 2), // 1.01 = 101%
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := DefaultParams()
			params.DecayRate = tt.rate

			err := params.Validate()
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), "decay rate")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNewParams_WithDecayFields(t *testing.T) {
	decayStartHeight := uint64(1000)
	decayRate := sdkmath.LegacyNewDecWithPrec(65, 3) // 6.5%

	params := NewParams(
		sdk.DefaultBondDenom,
		sdkmath.LegacyNewDecWithPrec(13, 2),
		sdkmath.LegacyNewDecWithPrec(20, 2),
		sdkmath.LegacyNewDecWithPrec(7, 2),
		sdkmath.LegacyNewDecWithPrec(67, 2),
		uint64(60*60*8766/5),
		decayStartHeight,
		decayRate,
	)

	require.Equal(t, decayStartHeight, params.DecayStartHeight)
	require.Equal(t, decayRate, params.DecayRate)

	err := params.Validate()
	require.NoError(t, err)
}
