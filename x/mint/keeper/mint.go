package keeper

import (
	"context"

	"cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

// MintFn defines the function that needs to be implemented in order to customize the minting process.
type MintFn func(ctx sdk.Context, k *Keeper) error

// MintFn runs the mintFn of the keeper.
func (k *Keeper) MintFn(ctx sdk.Context) error {
	return k.mintFn(ctx, k)
}

// DefaultMintFn returns a default mint function.
// The default MintFn has a requirement on staking as it uses bond to calculate inflation.
func DefaultMintFn(ic types.InflationCalculationFn) MintFn {
	return func(ctx sdk.Context, k *Keeper) error {
		// fetch stored minter & params
		minter, err := k.Minter.Get(ctx)
		if err != nil {
			return err
		}

		params, err := k.Params.Get(ctx)
		if err != nil {
			return err
		}

		// recalculate inflation rate
		totalStakingSupply, err := k.StakingTokenSupply(ctx)
		if err != nil {
			return err
		}

		bondedRatio, err := k.BondedRatio(ctx)
		if err != nil {
			return err
		}

		minter.Inflation = ic(ctx, minter, params, bondedRatio)
		minter.AnnualProvisions = minter.NextAnnualProvisions(params, totalStakingSupply)
		if err = k.Minter.Set(ctx, minter); err != nil {
			return err
		}

		// mint coins, update supply
		mintedCoin := minter.BlockProvision(params)
		mintedCoins := sdk.NewCoins(mintedCoin)

		err = k.MintCoins(ctx, mintedCoins)
		if err != nil {
			return err
		}

		// send the minted coins to the fee collector account
		err = k.AddCollectedFees(ctx, mintedCoins)
		if err != nil {
			return err
		}

		if mintedCoin.Amount.IsInt64() {
			defer telemetry.ModuleSetGauge(types.ModuleName, float32(mintedCoin.Amount.Int64()), "minted_tokens")
		}

		ctx.EventManager().EmitEvent(
			sdk.NewEvent(
				types.EventTypeMint,
				sdk.NewAttribute(types.AttributeKeyBondedRatio, bondedRatio.String()),
				sdk.NewAttribute(types.AttributeKeyInflation, minter.Inflation.String()),
				sdk.NewAttribute(types.AttributeKeyAnnualProvisions, minter.AnnualProvisions.String()),
				sdk.NewAttribute(sdk.AttributeKeyAmount, mintedCoin.Amount.String()),
			),
		)

		return nil
	}
}

// DeflationCalculationFn returns a custom InflationCalculationFn which applies continuous exponential decay to inflation.
// Formula: inflation_rate = base_rate × (1 - monthly_decay)^months_elapsed
// where months_elapsed = blocks_elapsed / blocks_per_month (continuous decimal value).
// The base_rate is the inflation rate calculated using the default method.
// Decay starts at DecayStartHeight and uses DecayRate from params.
func DeflationCalculationFn(ctx context.Context, minter types.Minter, params types.Params, bondedRatio math.LegacyDec) math.LegacyDec {
	// Calculate base inflation rate using default method
	baseRate := types.DefaultInflationCalculationFn(ctx, minter, params, bondedRatio)

	// Apply decay if enabled and we're past the start height
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	currentHeight := uint64(sdkCtx.BlockHeight())
	finalInflation := baseRate

	if params.DecayRate.IsPositive() && currentHeight >= params.DecayStartHeight {
		monthsInYear := uint64(12)
		blocksPerMonth := params.BlocksPerYear / monthsInYear
		blocksElapsed := currentHeight - params.DecayStartHeight

		if blocksPerMonth > 0 {
			// Compute months elapsed as a decimal for continuous decay
			monthsElapsed := math.LegacyNewDec(int64(blocksElapsed)).Quo(math.LegacyNewDec(int64(blocksPerMonth)))

			// Power() only accepts uint64, so decompose the exponent:
			// x^months = x^n × (x^(1/m))^r, where months = n + r/m
			// Note: we compute (x^(1/m))^r, NOT (x^r)^(1/m), because x^r underflows
			// to 0 for large r when x < 1, whereas x^(1/m) stays close to 1.
			n := uint64(monthsElapsed.TruncateInt64())
			r := blocksElapsed % blocksPerMonth

			decayFactor := math.LegacyOneDec().Sub(params.DecayRate)
			intPart := decayFactor.Power(n)
			perBlockFactor, err := decayFactor.ApproxRoot(blocksPerMonth) // x^(1/m)
			if err != nil {
				// ApproxRoot should never error, but if it does, return 0 inflation: chain keeps running, no new minting.
				return math.LegacyZeroDec()
			}
			fracPart := perBlockFactor.Power(r) // (x^(1/m))^r = x^(r/m)
			finalInflation = baseRate.Mul(intPart.Mul(fracPart))
		}
	}
	return finalInflation
}
