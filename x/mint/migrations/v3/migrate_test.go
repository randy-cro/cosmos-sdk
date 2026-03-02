package v3_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	"github.com/cosmos/cosmos-sdk/x/mint"
	v3 "github.com/cosmos/cosmos-sdk/x/mint/migrations/v3"
	"github.com/cosmos/cosmos-sdk/x/mint/types"
)

func TestMigrate(t *testing.T) {
	encCfg := moduletestutil.MakeTestEncodingConfig(mint.AppModuleBasic{})
	cdc := encCfg.Codec

	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	tKey := storetypes.NewTransientStoreKey("transient_test")
	ctx := testutil.DefaultContext(storeKey, tKey)
	kvStoreService := runtime.NewKVStoreService(storeKey)

	// Create a collections schema and params collection
	sb := collections.NewSchemaBuilder(kvStoreService)
	paramsCollection := collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc))
	_, err := sb.Build()
	require.NoError(t, err)

	// Set initial params (without DecayStartHeight, DecayRate, InflationMax, InflationMin)
	initialParams := types.DefaultParams()
	// Clear the new fields to simulate pre-migration state
	initialParams.DecayStartHeight = 0
	initialParams.DecayRate = math.LegacyDec{}

	err = paramsCollection.Set(ctx, initialParams)
	require.NoError(t, err)

	// Set block height to simulate upgrade height
	upgradeHeight := int64(10000)
	ctx = ctx.WithBlockHeight(upgradeHeight)

	// Run migration
	err = v3.Migrate(ctx, paramsCollection)
	require.NoError(t, err)

	// Verify migrated params
	migratedParams, err := paramsCollection.Get(ctx)
	require.NoError(t, err)

	// Check that DecayStartHeight is set to upgrade height
	require.Equal(t, uint64(upgradeHeight), migratedParams.DecayStartHeight, "DecayStartHeight should be set to upgrade height")

	// Check that DecayRate is set to 6.80%
	expectedDecayRate := math.LegacyNewDecWithPrec(680, 4) // 0.0680 = 6.80%
	require.True(t, migratedParams.DecayRate.Equal(expectedDecayRate), "DecayRate should be 6.80%%")

	// Check that InflationMax and InflationMin are set to 1%
	expectedInflation := math.LegacyNewDecWithPrec(1, 2) // 0.01 = 1%
	require.True(t, migratedParams.InflationMax.Equal(expectedInflation), "InflationMax should be 1%%")
	require.True(t, migratedParams.InflationMin.Equal(expectedInflation), "InflationMin should be 1%%")

	// Verify unchanged params
	require.Equal(t, initialParams.MintDenom, migratedParams.MintDenom)
	require.True(t, migratedParams.InflationRateChange.Equal(math.LegacyZeroDec()), "InflationRateChange should be 0")
	require.Equal(t, initialParams.GoalBonded, migratedParams.GoalBonded)
	require.Equal(t, initialParams.BlocksPerYear, migratedParams.BlocksPerYear)

	// Verify migrated params are valid
	err = migratedParams.Validate()
	require.NoError(t, err, "migrated params should be valid")
}
