package keeper

import (
	"github.com/cosmos/cosmos-sdk/x/mint/exported"
	v3 "github.com/cosmos/cosmos-sdk/x/mint/migrations/v3"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// Migrator is a struct for handling in-place state migrations.
type Migrator struct {
	keeper         Keeper
	legacySubspace exported.Subspace
}

// NewMigrator returns Migrator instance for the state migration.
func NewMigrator(k Keeper, ss exported.Subspace) Migrator {
	return Migrator{
		keeper:         k,
		legacySubspace: ss,
	}
}

// Migrate2to3 migrates the x/mint module state from the consensus version 2 to
// version 3. Specifically, it adds the DecayStartHeight and DecayRate fields
// to the existing params, setting DecayStartHeight to the upgrade height and
// DecayRate to 6.5% (0.065).
func (m Migrator) Migrate2to3(ctx sdk.Context) error {
	return v3.Migrate(ctx, m.keeper.Params)
}
