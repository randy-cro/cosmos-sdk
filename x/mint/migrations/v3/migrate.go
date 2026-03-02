package v3

import (
	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

// Migrate migrates the x/mint module state from the consensus version 2 to
// version 3.

//TODO: move this migration to POS instead
func Migrate(
	ctx sdk.Context,
	c collections.Item[types.Params],
) error {
	currParams, err := c.Get(ctx)
	if err != nil {
		return err
	}
	currParams.DecayStartHeight = uint64(ctx.BlockHeight())
	currParams.InflationMax = math.LegacyNewDecWithPrec(1, 2)  // 0.01 = 1%
	currParams.InflationMin = math.LegacyNewDecWithPrec(1, 2)  // 0.01 = 1%
	currParams.DecayRate = math.LegacyNewDecWithPrec(680, 4) // 0.0680 = 6.80%
	// Set inflation rate change for consistency with the new decay mechanism
	currParams.InflationRateChange = math.LegacyZeroDec()

	if err := currParams.Validate(); err != nil {
		return err
	}

	return c.Set(ctx, currParams)
}
