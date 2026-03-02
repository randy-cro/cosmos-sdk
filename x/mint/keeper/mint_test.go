package keeper_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/cosmos/cosmos-sdk/x/mint"
	"github.com/cosmos/cosmos-sdk/x/mint/keeper"
	minttestutil "github.com/cosmos/cosmos-sdk/x/mint/testutil"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

// MintFnTestSuite defines the integration test suite for minting.
type MintFnTestSuite struct {
	suite.Suite

	mintKeeper    keeper.Keeper
	ctx           sdk.Context
	stakingKeeper *minttestutil.MockStakingKeeper
	bankKeeper    *minttestutil.MockBankKeeper
}

// TestMintFnTestSuite runs the mint test suite.
func TestMintFnTestSuite(t *testing.T) {
	suite.Run(t, new(MintFnTestSuite))
}

// SetupTest sets up the context, KV store, and mocks.
func (s *MintFnTestSuite) SetupTest() {
	encCfg := moduletestutil.MakeTestEncodingConfig(mint.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	testCtx := testutil.DefaultContextWithDB(s.T(), key, storetypes.NewTransientStoreKey("transient_test"))
	s.ctx = testCtx.Ctx

	ctrl := gomock.NewController(s.T())
	accountKeeper := minttestutil.NewMockAccountKeeper(ctrl)
	s.bankKeeper = minttestutil.NewMockBankKeeper(ctrl)
	s.stakingKeeper = minttestutil.NewMockStakingKeeper(ctrl)

	// Return a dummy module address for the mint module.
	accountKeeper.EXPECT().GetModuleAddress(types.ModuleName).Return(sdk.AccAddress{}).AnyTimes()

	// Override the default mint function with our dummy inflation calculator.
	s.mintKeeper = keeper.NewKeeper(
		encCfg.Codec,
		storeService,
		s.stakingKeeper,
		accountKeeper,
		s.bankKeeper,
		authtypes.FeeCollectorName,
		authtypes.NewModuleAddress(govtypes.ModuleName).String(),
	)

	// Set default parameters.
	err := s.mintKeeper.Params.Set(s.ctx, types.DefaultParams())
	s.Require().NoError(err)

	// Set a known dummy minter in the store for deterministic behavior.
	s.Require().NoError(s.mintKeeper.Minter.Set(s.ctx, types.DefaultInitialMinter()))
}

// TestDefaultMintFn_Success tests the successful execution of the default mint function.
func (s *MintFnTestSuite) TestDefaultMintFn_Success() {
	// Set the staking keeper expectations.
	stakingSupply := math.NewInt(1_000_000_000)
	bondedRatio := math.LegacyNewDecWithPrec(50, 2) // 0.50
	s.stakingKeeper.EXPECT().StakingTokenSupply(s.ctx).Return(stakingSupply, nil).Times(1)
	s.stakingKeeper.EXPECT().BondedRatio(s.ctx).Return(bondedRatio, nil).Times(1)

	expectedCoins := sdk.NewCoins(sdk.NewCoin("stake", math.NewInt(20)))

	minter, err := s.mintKeeper.Minter.Get(s.ctx)
	s.Require().NoError(err)
	expectedInflation := types.DefaultInflationCalculationFn(context.TODO(), minter, types.DefaultParams(), bondedRatio)

	// Set bank keeper expectations for minting and fee collection.
	s.bankKeeper.EXPECT().MintCoins(s.ctx, types.ModuleName, expectedCoins).Return(nil).Times(1)
	s.bankKeeper.EXPECT().SendCoinsFromModuleToModule(s.ctx, types.ModuleName, authtypes.FeeCollectorName, expectedCoins).Return(nil).Times(1)

	// Call the mint function.
	err = s.mintKeeper.MintFn(s.ctx)
	s.Require().NoError(err)

	// Retrieve the updated minter from storage.
	updatedMinter, err := s.mintKeeper.Minter.Get(s.ctx)
	s.Require().NoError(err)

	// check that minter values are updated as expected
	s.Require().Equal(expectedInflation, updatedMinter.Inflation)
	s.Require().Equal(expectedInflation.MulInt(stakingSupply), updatedMinter.AnnualProvisions)

	// Optionally, verify that a mint event has been emitted.
	events := s.ctx.EventManager().Events()
	s.Require().True(slices.ContainsFunc(events, func(event sdk.Event) bool {
		return event.Type == types.EventTypeMint
	}), "expected a mint event to be emitted")
}

// customMintFn defines a custom minting function that overrides minter behavior.
func customMintFn(ctx sdk.Context, k *keeper.Keeper) error {
	// Retrieve the current minter and parameters.
	minter, err := k.Minter.Get(ctx)
	if err != nil {
		return err
	}
	_, err = k.Params.Get(ctx)
	if err != nil {
		return err
	}

	// Custom logic: override minter values.
	minter.Inflation = math.LegacyMustNewDecFromStr("0.1")
	minter.AnnualProvisions = math.LegacyMustNewDecFromStr("200")
	if err := k.Minter.Set(ctx, minter); err != nil {
		return err
	}

	// Instead of the default block provision, mint a custom coin.
	mintedCoin := sdk.NewCoin("custom", math.NewInt(50))
	mintedCoins := sdk.NewCoins(mintedCoin)

	// Execute bank keeper methods.
	if err := k.MintCoins(ctx, mintedCoins); err != nil {
		return err
	}
	if err := k.AddCollectedFees(ctx, mintedCoins); err != nil {
		return err
	}

	// Emit a custom event.
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		"custom_mint",
		sdk.NewAttribute("custom_attribute", "true"),
	))

	return nil
}

// TestCustomMintFn tests the custom mint function.
func (s *MintFnTestSuite) TestCustomMintFn() {
	// Reinitialize the keeper with the custom mint function.
	encCfg := moduletestutil.MakeTestEncodingConfig(mint.AppModuleBasic{})
	key := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(key)
	s.ctx = testutil.DefaultContextWithDB(s.T(), key, storetypes.NewTransientStoreKey("transient_test")).Ctx

	ctrl := gomock.NewController(s.T())
	accountKeeper := minttestutil.NewMockAccountKeeper(ctrl)
	// Use fresh mocks for account keeper if needed.
	accountKeeper.EXPECT().GetModuleAddress(types.ModuleName).Return(sdk.AccAddress{}).AnyTimes()

	// Reuse the existing stakingKeeper and bankKeeper from the suite.
	s.mintKeeper = keeper.NewKeeper(
		encCfg.Codec,
		storeService,
		s.stakingKeeper,
		accountKeeper,
		s.bankKeeper,
		authtypes.FeeCollectorName,
		authtypes.NewModuleAddress(govtypes.ModuleName).String(),
		keeper.WithMintFn(customMintFn),
	)

	// Set default parameters and initial minter.
	err := s.mintKeeper.Params.Set(s.ctx, types.DefaultParams())
	s.Require().NoError(err)
	s.Require().NoError(s.mintKeeper.Minter.Set(s.ctx, types.DefaultInitialMinter()))

	// Expect bank keeper calls to be made for the custom minted coin.
	expectedCoins := sdk.NewCoins(sdk.NewCoin("custom", math.NewInt(50)))
	s.bankKeeper.EXPECT().MintCoins(s.ctx, types.ModuleName, expectedCoins).Return(nil).Times(1)
	s.bankKeeper.EXPECT().SendCoinsFromModuleToModule(s.ctx, types.ModuleName, authtypes.FeeCollectorName, expectedCoins).Return(nil).Times(1)

	// Call the custom mint function.
	err = s.mintKeeper.MintFn(s.ctx)
	s.Require().NoError(err)

	// Retrieve and verify the updated minter values.
	storedMinter, err := s.mintKeeper.Minter.Get(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(math.LegacyMustNewDecFromStr("0.1"), storedMinter.Inflation)
	s.Require().Equal(math.LegacyMustNewDecFromStr("200"), storedMinter.AnnualProvisions)

	// Check that the custom mint event was emitted.
	events := s.ctx.EventManager().Events()
	s.Require().True(slices.ContainsFunc(events, func(event sdk.Event) bool {
		return event.Type == "custom_mint"
	}), "expected custom_mint event to be emitted")
}

// TestDeflationCalculationFn_NoDecay tests that deflation calculation works when decay is disabled.
func (s *MintFnTestSuite) TestDeflationCalculationFn_NoDecay() {
	params := types.DefaultParams()
	params.DecayRate = math.LegacyZeroDec() // Disable decay
	params.DecayStartHeight = 1000

	err := s.mintKeeper.Params.Set(s.ctx, params)
	s.Require().NoError(err)

	minter := types.DefaultInitialMinter()
	bondedRatio := math.LegacyNewDecWithPrec(50, 2) // 0.50
	baseInflation := types.DefaultInflationCalculationFn(s.ctx, minter, params, bondedRatio)

	// Before decay start height
	s.ctx = s.ctx.WithBlockHeight(500)
	inflation := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	s.Require().Equal(baseInflation, inflation, "inflation should match default when decay is disabled")

	// After decay start height but decay disabled
	s.ctx = s.ctx.WithBlockHeight(2000)
	inflation = keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	s.Require().Equal(baseInflation, inflation, "inflation should match default when decay is disabled")
}

// TestDeflationCalculationFn_WithDecay tests that deflation calculation applies decay correctly.
func (s *MintFnTestSuite) TestDeflationCalculationFn_WithDecay() {
	params := types.DefaultParams()
	params.DecayStartHeight = 1000
	params.DecayRate = math.LegacyNewDecWithPrec(65, 3) // 6.5% monthly decay
	params.BlocksPerYear = 6307200                      // ~5 second blocks - divisible by 12 for test later

	err := s.mintKeeper.Params.Set(s.ctx, params)
	s.Require().NoError(err)

	minter := types.DefaultInitialMinter()
	bondedRatio := math.LegacyNewDecWithPrec(50, 2) // 0.50
	baseInflation := types.DefaultInflationCalculationFn(s.ctx, minter, params, bondedRatio)

	// Before decay start height - should use base rate
	s.ctx = s.ctx.WithBlockHeight(500)
	inflationBefore := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	s.Require().Equal(baseInflation, inflationBefore, "inflation should equal base before decay starts")

	// At decay start height - should still use base rate (no months elapsed)
	s.ctx = s.ctx.WithBlockHeight(1000)
	inflationAtStart := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	s.Require().Equal(baseInflation, inflationAtStart, "inflation should equal base at decay start (0 months elapsed)")

	// After 1 month - should apply decay
	blocksPerMonth := params.BlocksPerYear / 12
	s.ctx = s.ctx.WithBlockHeight(1000 + int64(blocksPerMonth))
	inflationAfter1Month := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	expectedDecayFactor := math.LegacyOneDec().Sub(params.DecayRate) // (1 - 0.065) = 0.935
	expectedInflation := baseInflation.Mul(expectedDecayFactor)

	// Calculate the difference and verify it's within acceptable precision
	diff := inflationAfter1Month.Sub(expectedInflation).Abs()
	// Use a tighter tolerance: 1e-12 for single month calculation
	maxTolerance := math.LegacyNewDecWithPrec(1, 12)
	s.Require().True(
		diff.LT(maxTolerance),
		"inflation after 1 month should be base * 0.935, got %s, expected %s, difference: %s (tolerance: %s)",
		inflationAfter1Month, expectedInflation, diff, maxTolerance,
	)

	// After 12 months - should apply decay^12
	s.ctx = s.ctx.WithBlockHeight(1000 + int64(blocksPerMonth*12))
	inflationAfter12Months := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	expectedDecayFactor12 := expectedDecayFactor.Power(12)
	expectedInflation12 := baseInflation.Mul(expectedDecayFactor12)

	// Calculate the difference and verify it's within acceptable precision
	diff12 := inflationAfter12Months.Sub(expectedInflation12).Abs()
	// Use tolerance: 1e-10 for 12-month calculation (Power() may accumulate small errors)
	maxTolerance12 := math.LegacyNewDecWithPrec(1, 10)
	s.Require().True(
		diff12.LT(maxTolerance12),
		"inflation after 12 months should be base * (0.935)^12, got %s, expected %s, difference: %s (tolerance: %s)",
		inflationAfter12Months, expectedInflation12, diff12, maxTolerance12,
	)
}

// TestDeflationCalculationFn_FractionalMonths tests that deflation calculation works correctly with fractional months.
func (s *MintFnTestSuite) TestDeflationCalculationFn_FractionalMonths() {
	params := types.DefaultParams()
	params.DecayStartHeight = 1000
	params.DecayRate = math.LegacyNewDecWithPrec(65, 3) // 6.5% monthly decay
	params.BlocksPerYear = 6307200                      // ~5 second blocks - divisible by 12

	err := s.mintKeeper.Params.Set(s.ctx, params)
	s.Require().NoError(err)

	minter := types.DefaultInitialMinter()
	bondedRatio := math.LegacyNewDecWithPrec(50, 2) // 0.50
	baseInflation := types.DefaultInflationCalculationFn(s.ctx, minter, params, bondedRatio)

	blocksPerMonth := params.BlocksPerYear / 12
	decayFactor := math.LegacyOneDec().Sub(params.DecayRate) // (1 - 0.065) = 0.935

	// Test 0.5 months (half a month)
	halfMonthBlocks := blocksPerMonth / 2
	s.ctx = s.ctx.WithBlockHeight(1000 + int64(halfMonthBlocks))
	inflationAfterHalfMonth := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)

	// Expected: base * decayFactor^0.5
	// Using decomposition: n=0, r=halfMonthBlocks
	// decayFactor^0.5 = (decayFactor^(1/blocksPerMonth))^halfMonthBlocks
	// Note: (x^r)^(1/m) underflows for large r; use (x^(1/m))^r instead.
	perBlockFactor, err := decayFactor.ApproxRoot(blocksPerMonth)
	s.Require().NoError(err)
	expectedDecayFactorHalfRoot := perBlockFactor.Power(halfMonthBlocks)
	expectedInflationHalf := baseInflation.Mul(expectedDecayFactorHalfRoot)

	diffHalf := inflationAfterHalfMonth.Sub(expectedInflationHalf).Abs()
	maxToleranceHalf := math.LegacyNewDecWithPrec(1, 10) // ApproxRoot may have some error
	s.Require().True(
		diffHalf.LT(maxToleranceHalf),
		"inflation after 0.5 months should be base * decayFactor^0.5, got %s, expected %s, difference: %s (tolerance: %s)",
		inflationAfterHalfMonth, expectedInflationHalf, diffHalf, maxToleranceHalf,
	)

	// Test 1.5 months
	oneAndHalfMonthBlocks := blocksPerMonth + halfMonthBlocks
	s.ctx = s.ctx.WithBlockHeight(1000 + int64(oneAndHalfMonthBlocks))
	inflationAfterOneAndHalfMonth := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)

	// Expected: base * decayFactor^1.5 = base * decayFactor^1 * decayFactor^0.5
	expectedDecayFactor1_5 := decayFactor.Power(1).Mul(expectedDecayFactorHalfRoot)
	expectedInflation1_5 := baseInflation.Mul(expectedDecayFactor1_5)

	diff1_5 := inflationAfterOneAndHalfMonth.Sub(expectedInflation1_5).Abs()
	maxTolerance1_5 := math.LegacyNewDecWithPrec(1, 10)
	s.Require().True(
		diff1_5.LT(maxTolerance1_5),
		"inflation after 1.5 months should be base * decayFactor^1.5, got %s, expected %s, difference: %s (tolerance: %s)",
		inflationAfterOneAndHalfMonth, expectedInflation1_5, diff1_5, maxTolerance1_5,
	)

	// Decay decreases inflation over time, so: inflation(1.5) < inflation(0.5) < initial base inflation
	s.ctx = s.ctx.WithBlockHeight(1000) // 0 months
	initialBaseInflation := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)

	s.Require().True(
		inflationAfterHalfMonth.LT(initialBaseInflation),
		"inflation at 0.5 months should be less than initial base inflation",
	)
	s.Require().True(
		inflationAfterOneAndHalfMonth.LT(inflationAfterHalfMonth),
		"inflation at 1.5 months should be less than inflation at 0.5 months",
	)
}

// TestDeflationCalculationFn_EdgeCases tests edge cases for fractional month calculations.
func (s *MintFnTestSuite) TestDeflationCalculationFn_EdgeCases() {
	params := types.DefaultParams()
	params.DecayStartHeight = 1000
	params.DecayRate = math.LegacyNewDecWithPrec(10, 2) // 10% monthly decay
	params.BlocksPerYear = 6307200                      // ~5 second blocks

	err := s.mintKeeper.Params.Set(s.ctx, params)
	s.Require().NoError(err)

	minter := types.DefaultInitialMinter()
	bondedRatio := math.LegacyNewDecWithPrec(50, 2)
	baseInflation := types.DefaultInflationCalculationFn(s.ctx, minter, params, bondedRatio)

	blocksPerMonth := params.BlocksPerYear / 12
	decayFactor := math.LegacyOneDec().Sub(params.DecayRate) // 0.90

	// Test at exactly decay start height (0 months elapsed)
	s.ctx = s.ctx.WithBlockHeight(1000)
	inflationAtStart := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)
	s.Require().Equal(baseInflation, inflationAtStart, "inflation at start should equal base (0 months elapsed)")

	// Test with very small elapsed time (1 block)
	s.ctx = s.ctx.WithBlockHeight(1001)
	inflationAfter1Block := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)

	// Should have minimal decay: decayFactor^(1/blocksPerMonth)
	// Use (x^(1/m))^r to match the implementation's stable formula.
	perBlockFactorEdge, err := decayFactor.ApproxRoot(blocksPerMonth)
	s.Require().NoError(err)
	expectedInflationMinimal := baseInflation.Mul(perBlockFactorEdge.Power(1))

	diffMinimal := inflationAfter1Block.Sub(expectedInflationMinimal).Abs()
	maxToleranceMinimal := math.LegacyNewDecWithPrec(1, 10)
	s.Require().True(
		diffMinimal.LT(maxToleranceMinimal),
		"inflation after 1 block should have minimal decay, got %s, expected %s, difference: %s",
		inflationAfter1Block, expectedInflationMinimal, diffMinimal,
	)

	// Verify inflation decreases as time passes
	s.Require().True(
		inflationAfter1Block.LT(inflationAtStart),
		"inflation should decrease as time passes: %s < %s",
		inflationAfter1Block, inflationAtStart,
	)
}

// TestDeflationCalculationFn_SupplyCap tests that circulating supply doesn't exceed 100B tokens.
func (s *MintFnTestSuite) TestDeflationCalculationFn_SupplyCap() {
	// Set up params with decay enabled
	params := types.DefaultParams()
	params.DecayStartHeight = 1
	params.InflationRateChange = math.LegacyMustNewDecFromStr("0.1") // 10%
	params.InflationMax = math.LegacyMustNewDecFromStr("0.01")       // 1%
	params.InflationMin = math.LegacyMustNewDecFromStr("0.01")       // 1%
	params.DecayRate = math.LegacyMustNewDecFromStr("0.0680")        // 6.80% monthly decay
	params.GoalBonded = math.LegacyMustNewDecFromStr("0.60")
	err := s.mintKeeper.Params.Set(s.ctx, params)
	s.Require().NoError(err)

	totalSupply := math.NewInt(99_045_307_761).Mul(math.NewInt(100_000_000))                         // 99B * 10^8
	burnedSupply := math.NewInt(420_000_000).Mul(math.NewInt(100_000_000))                           // 42B * 10^8
	bondedTokens := math.NewInt(17_043_807_749).Mul(math.NewInt(100_000_000))                        //  17B * 10^8
	circulatingSupply := totalSupply.Sub(burnedSupply)                                               // (99B - 42B) * 10^8
	bondedRatio := math.LegacyNewDecFromInt(bondedTokens).Quo(math.LegacyNewDecFromInt(totalSupply)) // ~17% bonded

	minter := types.DefaultInitialMinter()
	minter.Inflation = math.LegacyNewDecWithPrec(1, 2) // Start at 1% inflation

	// Simulate many blocks (e.g., 10 years worth)
	totalBlocks := int64(params.BlocksPerYear * 10) // 10 years
	blocksPerWeek := int64(params.BlocksPerYear / 52)
	maxSupply := math.NewInt(100_000_000_000).Mul(math.NewInt(100_000_000)) // 100B * 10^8

	s.ctx = s.ctx.WithBlockHeight(0)

	// Track supply over time
	for block := int64(1); block <= totalBlocks; block += blocksPerWeek {
		s.ctx = s.ctx.WithBlockHeight(block)

		// Calculate inflation with decay
		inflation := keeper.DeflationCalculationFn(s.ctx, minter, params, bondedRatio)

		minter.Inflation = inflation

		// Update annual provisions
		minter.AnnualProvisions = minter.NextAnnualProvisions(params, totalSupply)

		// Calculate block provision
		blockProvision := minter.BlockProvision(params).Amount

		// Calculate weekly provision
		weeklyProvision := blockProvision.Mul(math.NewInt(blocksPerWeek))

		// Update supply
		circulatingSupply = circulatingSupply.Add(weeklyProvision)
		totalSupply = totalSupply.Add(weeklyProvision)

		// Ensure we never exceed 100B
		s.Require().True(
			circulatingSupply.LTE(maxSupply),
			"supply exceeded 100B at block %d: %s > %s",
			block, circulatingSupply, maxSupply,
		)
	}

	// Final check: supply should be at or below 100B
	s.Require().True(
		circulatingSupply.LTE(maxSupply),
		"final supply exceeded 100B: %s > %s",
		circulatingSupply, maxSupply,
	)
}
