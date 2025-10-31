package keeper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	cmtprotocrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	gogotypes "github.com/cosmos/gogoproto/types"

	corestore "cosmossdk.io/core/store"
	errorsmod "cosmossdk.io/errors"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/staking/types"
)

// GetValidator gets a single validator
func (k Keeper) GetValidator(ctx context.Context, addr sdk.ValAddress) (validator types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	value, err := store.Get(types.GetValidatorKey(addr))
	if err != nil {
		return validator, err
	}

	if value == nil {
		return validator, types.ErrNoValidatorFound
	}

	return types.UnmarshalValidator(k.cdc, value)
}

func (k Keeper) mustGetValidator(ctx context.Context, addr sdk.ValAddress) types.Validator {
	validator, err := k.GetValidator(ctx, addr)
	if err != nil {
		panic(fmt.Sprintf("validator record not found for address: %X\n", addr))
	}

	return validator
}

// GetValidatorByConsAddr gets a single validator by consensus address
func (k Keeper) GetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) (validator types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	opAddr, err := store.Get(types.GetValidatorByConsAddrKey(consAddr))
	if err != nil {
		return validator, err
	}

	if opAddr == nil {
		return validator, types.ErrNoValidatorFound
	}

	return k.GetValidator(ctx, opAddr)
}

func (k Keeper) mustGetValidatorByConsAddr(ctx context.Context, consAddr sdk.ConsAddress) types.Validator {
	validator, err := k.GetValidatorByConsAddr(ctx, consAddr)
	if err != nil {
		panic(fmt.Errorf("validator with consensus-Address %s not found", consAddr))
	}

	return validator
}

// SetValidator sets the main record holding validator details
func (k Keeper) SetValidator(ctx context.Context, validator types.Validator) error {
	store := k.storeService.OpenKVStore(ctx)
	bz := types.MustMarshalValidator(k.cdc, &validator)
	str, err := k.ValidatorAddressCodec().StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorKey(str), bz)
}

// SetValidatorByConsAddr sets a validator by conesensus address
func (k Keeper) SetValidatorByConsAddr(ctx context.Context, validator types.Validator) error {
	consPk, err := validator.GetConsAddr()
	if err != nil {
		return err
	}
	store := k.storeService.OpenKVStore(ctx)

	bz, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}

	return store.Set(types.GetValidatorByConsAddrKey(consPk), bz)
}

// SetValidatorByPowerIndex sets a validator by power index
func (k Keeper) SetValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	// jailed validators are not kept in the power index
	if validator.Jailed {
		return nil
	}

	store := k.storeService.OpenKVStore(ctx)
	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec), str)
}

// DeleteValidatorByPowerIndex deletes a record by power index
func (k Keeper) DeleteValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec))
}

// SetNewValidatorByPowerIndex adds new entry by power index
func (k Keeper) SetNewValidatorByPowerIndex(ctx context.Context, validator types.Validator) error {
	store := k.storeService.OpenKVStore(ctx)
	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}
	return store.Set(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec), str)
}

// AddValidatorTokensAndShares updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) AddValidatorTokensAndShares(ctx context.Context, validator types.Validator,
	tokensToAdd math.Int,
) (valOut types.Validator, addedShares math.LegacyDec, err error) {
	err = k.DeleteValidatorByPowerIndex(ctx, validator)
	if err != nil {
		return valOut, addedShares, err
	}

	validator, addedShares = validator.AddTokensFromDel(tokensToAdd)
	err = k.SetValidator(ctx, validator)
	if err != nil {
		return validator, addedShares, err
	}

	err = k.SetValidatorByPowerIndex(ctx, validator)
	return validator, addedShares, err
}

// RemoveValidatorTokensAndShares updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) RemoveValidatorTokensAndShares(ctx context.Context, validator types.Validator,
	sharesToRemove math.LegacyDec,
) (valOut types.Validator, removedTokens math.Int, err error) {
	err = k.DeleteValidatorByPowerIndex(ctx, validator)
	if err != nil {
		return valOut, removedTokens, err
	}
	validator, removedTokens = validator.RemoveDelShares(sharesToRemove)
	err = k.SetValidator(ctx, validator)
	if err != nil {
		return validator, removedTokens, err
	}

	err = k.SetValidatorByPowerIndex(ctx, validator)
	return validator, removedTokens, err
}

// RemoveValidatorTokens updates the tokens of an existing validator, updates the validators power index key
func (k Keeper) RemoveValidatorTokens(ctx context.Context,
	validator types.Validator, tokensToRemove math.Int,
) (types.Validator, error) {
	if err := k.DeleteValidatorByPowerIndex(ctx, validator); err != nil {
		return validator, err
	}

	validator = validator.RemoveTokens(tokensToRemove)
	if err := k.SetValidator(ctx, validator); err != nil {
		return validator, err
	}

	if err := k.SetValidatorByPowerIndex(ctx, validator); err != nil {
		return validator, err
	}

	return validator, nil
}

// UpdateValidatorCommission attempts to update a validator's commission rate.
// An error is returned if the new commission rate is invalid.
func (k Keeper) UpdateValidatorCommission(ctx context.Context,
	validator types.Validator, newRate math.LegacyDec,
) (types.Commission, error) {
	commission := validator.Commission
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	blockTime := sdkCtx.BlockHeader().Time

	if err := commission.ValidateNewRate(newRate, blockTime); err != nil {
		return commission, err
	}

	minCommissionRate, err := k.MinCommissionRate(ctx)
	if err != nil {
		return commission, err
	}

	if newRate.LT(minCommissionRate) {
		return commission, fmt.Errorf("cannot set validator commission to less than minimum rate of %s", minCommissionRate)
	}

	commission.Rate = newRate
	commission.UpdateTime = blockTime

	return commission, nil
}

// RemoveValidator removes the validator record and associated indexes
// except for the bonded validator index which is only handled in ApplyAndReturnTendermintUpdates
func (k Keeper) RemoveValidator(ctx context.Context, address sdk.ValAddress) error {
	// first retrieve the old validator record
	validator, err := k.GetValidator(ctx, address)
	if errors.Is(err, types.ErrNoValidatorFound) {
		return nil
	}

	if !validator.IsUnbonded() {
		return types.ErrBadRemoveValidator.Wrap("cannot call RemoveValidator on bonded or unbonding validators")
	}

	if validator.Tokens.IsPositive() {
		return types.ErrBadRemoveValidator.Wrap("attempting to remove a validator which still contains tokens")
	}

	valConsAddr, err := validator.GetConsAddr()
	if err != nil {
		return err
	}

	// delete the old validator record
	store := k.storeService.OpenKVStore(ctx)
	if err = store.Delete(types.GetValidatorKey(address)); err != nil {
		return err
	}

	if err = store.Delete(types.GetValidatorByConsAddrKey(valConsAddr)); err != nil {
		return err
	}

	if err = store.Delete(types.GetValidatorsByPowerIndexKey(validator, k.PowerReduction(ctx), k.validatorAddressCodec)); err != nil {
		return err
	}

	str, err := k.validatorAddressCodec.StringToBytes(validator.GetOperator())
	if err != nil {
		return err
	}

	if err := k.Hooks().AfterValidatorRemoved(ctx, valConsAddr, str); err != nil {
		k.Logger(ctx).Error("error in after validator removed hook", "error", err)
	}

	return nil
}

// get groups of validators

// GetAllValidators gets the set of all validators with no limits, used during genesis dump
func (k Keeper) GetAllValidators(ctx context.Context) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)

	iterator, err := store.Iterator(types.ValidatorsKey, storetypes.PrefixEndBytes(types.ValidatorsKey))
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	for ; iterator.Valid(); iterator.Next() {
		validator, err := types.UnmarshalValidator(k.cdc, iterator.Value())
		if err != nil {
			return nil, err
		}
		validators = append(validators, validator)
	}

	return validators, nil
}

// GetValidators returns a given amount of all the validators
func (k Keeper) GetValidators(ctx context.Context, maxRetrieve uint32) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)
	validators = make([]types.Validator, maxRetrieve)

	iterator, err := store.Iterator(types.ValidatorsKey, storetypes.PrefixEndBytes(types.ValidatorsKey))
	if err != nil {
		return nil, err
	}

	i := 0
	for ; iterator.Valid() && i < int(maxRetrieve); iterator.Next() {
		validator, err := types.UnmarshalValidator(k.cdc, iterator.Value())
		if err != nil {
			return nil, err
		}
		validators[i] = validator
		i++
	}

	return validators[:i], nil // trim if the array length < maxRetrieve
}

// GetBondedValidatorsByPower gets the current group of bonded validators sorted by power-rank
func (k Keeper) GetBondedValidatorsByPower(ctx context.Context) ([]types.Validator, error) {
	maxValidators, err := k.MaxValidators(ctx)
	if err != nil {
		return nil, err
	}
	validators := make([]types.Validator, maxValidators)

	iterator, err := k.ValidatorsPowerStoreIterator(ctx)
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	i := 0
	for ; iterator.Valid() && i < int(maxValidators); iterator.Next() {
		address := iterator.Value()
		validator := k.mustGetValidator(ctx, address)

		if validator.IsBonded() {
			validators[i] = validator
			i++
		}
	}

	return validators[:i], nil // trim
}

// ValidatorsPowerStoreIterator returns an iterator for the current validator power store
func (k Keeper) ValidatorsPowerStoreIterator(ctx context.Context) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.ReverseIterator(types.ValidatorsByPowerIndexKey, storetypes.PrefixEndBytes(types.ValidatorsByPowerIndexKey))
}

// Last Validator Index

// GetLastValidatorPower loads the last validator power.
// Returns zero if the operator was not a validator last block.
func (k Keeper) GetLastValidatorPower(ctx context.Context, operator sdk.ValAddress) (power int64, err error) {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := store.Get(types.GetLastValidatorPowerKey(operator))
	if err != nil {
		return 0, err
	}

	if bz == nil {
		return 0, nil
	}

	intV := gogotypes.Int64Value{}
	err = k.cdc.Unmarshal(bz, &intV)
	if err != nil {
		return 0, err
	}

	return intV.GetValue(), nil
}

// SetLastValidatorPower sets the last validator power.
func (k Keeper) SetLastValidatorPower(ctx context.Context, operator sdk.ValAddress, power int64) error {
	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&gogotypes.Int64Value{Value: power})
	if err != nil {
		return err
	}
	return store.Set(types.GetLastValidatorPowerKey(operator), bz)
}

// DeleteLastValidatorPower deletes the last validator power.
func (k Keeper) DeleteLastValidatorPower(ctx context.Context, operator sdk.ValAddress) error {
	store := k.storeService.OpenKVStore(ctx)
	return store.Delete(types.GetLastValidatorPowerKey(operator))
}

// lastValidatorsIterator returns an iterator for the consensus validators in the last block
func (k Keeper) LastValidatorsIterator(ctx context.Context) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.Iterator(types.LastValidatorPowerKey, storetypes.PrefixEndBytes(types.LastValidatorPowerKey))
}

// IterateLastValidatorPowers iterates over last validator powers.
func (k Keeper) IterateLastValidatorPowers(ctx context.Context, handler func(operator sdk.ValAddress, power int64) (stop bool)) error {
	iter, err := k.LastValidatorsIterator(ctx)
	if err != nil {
		return err
	}

	for ; iter.Valid(); iter.Next() {
		addr := sdk.ValAddress(types.AddressFromLastValidatorPowerKey(iter.Key()))
		intV := &gogotypes.Int64Value{}

		if err = k.cdc.Unmarshal(iter.Value(), intV); err != nil {
			return err
		}

		if handler(addr, intV.GetValue()) {
			break
		}
	}

	return nil
}

// GetLastValidators gets the group of the bonded validators
func (k Keeper) GetLastValidators(ctx context.Context) (validators []types.Validator, err error) {
	store := k.storeService.OpenKVStore(ctx)

	// add the actual validator power sorted store
	maxValidators, err := k.MaxValidators(ctx)
	if err != nil {
		return nil, err
	}
	validators = make([]types.Validator, maxValidators)

	iterator, err := store.Iterator(types.LastValidatorPowerKey, storetypes.PrefixEndBytes(types.LastValidatorPowerKey))
	if err != nil {
		return nil, err
	}
	defer iterator.Close()

	i := 0
	for ; iterator.Valid(); iterator.Next() {
		// sanity check
		if i >= int(maxValidators) {
			panic("more validators than maxValidators found")
		}

		address := types.AddressFromLastValidatorPowerKey(iterator.Key())
		validator, err := k.GetValidator(ctx, address)
		if err != nil {
			return nil, err
		}

		validators[i] = validator
		i++
	}

	return validators[:i], nil // trim
}

// GetUnbondingValidators returns a slice of mature validator addresses that
// complete their unbonding at a given time and height.
func (k Keeper) GetUnbondingValidators(ctx context.Context, endTime time.Time, endHeight int64) ([]string, error) {
	if k.cache != nil {
		cachedAddrs, err := k.cache.GetUnbondingValidatorsQueueEntry(ctx, endTime, endHeight)
		if err == nil {
			k.Logger(ctx).Info("GetUnbondingValidators: retrieved from cache",
				"time", endTime,
				"height", endHeight,
				"count", len(cachedAddrs),
				"validators", cachedAddrs)
			return cachedAddrs, nil
		}
		k.Logger(ctx).Error("GetUnbondingValidators from cache failed. Error: %s", err)
	}

	store := k.storeService.OpenKVStore(ctx)

	bz, err := store.Get(types.GetValidatorQueueKey(endTime, endHeight))
	if err != nil {
		return nil, err
	}

	if bz == nil {
		k.Logger(ctx).Info("GetUnbondingValidators: no validators in store",
			"time", endTime,
			"height", endHeight)
		return []string{}, nil
	}

	addrs := types.ValAddresses{}
	if err = k.cdc.Unmarshal(bz, &addrs); err != nil {
		return nil, err
	}

	k.Logger(ctx).Info("GetUnbondingValidators: retrieved from store",
		"time", endTime,
		"height", endHeight,
		"count", len(addrs.Addresses),
		"validators", addrs.Addresses)

	return addrs.Addresses, nil
}

// SetUnbondingValidatorsQueue sets a given slice of validator addresses into
// the unbonding validator queue by a given height and time.
func (k Keeper) SetUnbondingValidatorsQueue(ctx context.Context, endTime time.Time, endHeight int64, addrs []string) error {
	k.Logger(ctx).Info("SetUnbondingValidatorsQueue: setting validators",
		"time", endTime,
		"height", endHeight,
		"count", len(addrs),
		"validators", addrs)

	store := k.storeService.OpenKVStore(ctx)
	bz, err := k.cdc.Marshal(&types.ValAddresses{Addresses: addrs})
	if err != nil {
		return err
	}
	err = store.Set(types.GetValidatorQueueKey(endTime, endHeight), bz)
	if err != nil {
		return err
	}

	if k.cache != nil {
		err = k.cache.SetUnbondingValidatorQueueEntry(ctx, types.GetCacheValidatorQueueKey(endTime, endHeight), addrs)
		if err != nil {
			k.Logger(ctx).Error("SetUnbondingValidatorsQueue: cache write failed",
				"error", err,
				"time", endTime,
				"height", endHeight)
		} else {
			k.Logger(ctx).Info("SetUnbondingValidatorsQueue: successfully wrote to cache",
				"time", endTime,
				"height", endHeight,
				"validators", addrs)
		}
	}
	return nil
}

// InsertUnbondingValidatorQueue inserts a given unbonding validator address into
// the unbonding validator queue for a given height and time.
func (k Keeper) InsertUnbondingValidatorQueue(ctx context.Context, val types.Validator) error {
	k.Logger(ctx).Info("InsertUnbondingValidatorQueue: adding validator to unbonding queue",
		"validator", val.OperatorAddress,
		"unbonding_time", val.UnbondingTime,
		"unbonding_height", val.UnbondingHeight,
		"status", val.Status.String())

	addrs, err := k.GetUnbondingValidators(ctx, val.UnbondingTime, val.UnbondingHeight)
	if err != nil {
		k.Logger(ctx).Error("InsertUnbondingValidatorQueue: failed to get existing validators",
			"error", err,
			"validator", val.OperatorAddress)
		return err
	}

	k.Logger(ctx).Info("InsertUnbondingValidatorQueue: current queue before insertion",
		"existing_count", len(addrs),
		"existing_validators", addrs)

	addrs = append(addrs, val.OperatorAddress)

	k.Logger(ctx).Info("InsertUnbondingValidatorQueue: new queue after insertion",
		"new_count", len(addrs),
		"new_validators", addrs)

	return k.SetUnbondingValidatorsQueue(ctx, val.UnbondingTime, val.UnbondingHeight, addrs)
}

// DeleteValidatorQueueTimeSlice deletes all entries in the queue indexed by a
// given height and time.
func (k Keeper) DeleteValidatorQueueTimeSlice(ctx context.Context, endTime time.Time, endHeight int64) error {
	k.Logger(ctx).Info("DeleteValidatorQueueTimeSlice: deleting entire time slice",
		"time", endTime,
		"height", endHeight)

	store := k.storeService.OpenKVStore(ctx)
	err := store.Delete(types.GetValidatorQueueKey(endTime, endHeight))
	if err != nil {
		k.Logger(ctx).Error("DeleteValidatorQueueTimeSlice: failed to delete from store",
			"error", err,
			"time", endTime,
			"height", endHeight)
		return err
	}
	if k.cache != nil {
		k.cache.DeleteUnbondingValidatorQueueEntry(types.GetCacheValidatorQueueKey(endTime, endHeight))
		k.Logger(ctx).Info("DeleteValidatorQueueTimeSlice: deleted from cache",
			"time", endTime,
			"height", endHeight)
	}
	return nil
}

// DeleteValidatorQueue removes a validator by address from the unbonding queue
// indexed by a given height and time.
func (k Keeper) DeleteValidatorQueue(ctx context.Context, val types.Validator) error {
	k.Logger(ctx).Info("DeleteValidatorQueue: removing validator from unbonding queue",
		"validator", val.OperatorAddress,
		"unbonding_time", val.UnbondingTime,
		"unbonding_height", val.UnbondingHeight)

	addrs, err := k.GetUnbondingValidators(ctx, val.UnbondingTime, val.UnbondingHeight)
	if err != nil {
		k.Logger(ctx).Error("DeleteValidatorQueue: failed to get validators",
			"error", err,
			"validator", val.OperatorAddress)
		return err
	}

	k.Logger(ctx).Info("DeleteValidatorQueue: current queue before deletion",
		"existing_count", len(addrs),
		"existing_validators", addrs)

	newAddrs := []string{}

	// since address string may change due to Bech32 prefix change, we parse the addresses into bytes
	// format for normalization
	deletingAddr, err := k.validatorAddressCodec.StringToBytes(val.OperatorAddress)
	if err != nil {
		return err
	}

	for _, addr := range addrs {
		storedAddr, err := k.validatorAddressCodec.StringToBytes(addr)
		if err != nil {
			// even if we don't error here, it will error in UnbondAllMatureValidators at unbond time
			return err
		}
		if !bytes.Equal(storedAddr, deletingAddr) {
			newAddrs = append(newAddrs, addr)
		}
	}

	k.Logger(ctx).Info("DeleteValidatorQueue: new queue after deletion",
		"new_count", len(newAddrs),
		"new_validators", newAddrs,
		"removed", len(addrs)-len(newAddrs))

	if len(newAddrs) == 0 {
		k.Logger(ctx).Info("DeleteValidatorQueue: queue empty, deleting entire time slice",
			"time", val.UnbondingTime,
			"height", val.UnbondingHeight)
		return k.DeleteValidatorQueueTimeSlice(ctx, val.UnbondingTime, val.UnbondingHeight)
	}

	return k.SetUnbondingValidatorsQueue(ctx, val.UnbondingTime, val.UnbondingHeight, newAddrs)
}

// ValidatorQueueIteratorAll gets all the validators that are unbonding
func (k Keeper) ValidatorQueueIteratorAll(ctx context.Context) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.Iterator(types.ValidatorQueueKey, storetypes.PrefixEndBytes(types.ValidatorQueueKey))
}

// ValidatorQueueIterator returns an interator ranging over validators that are
// unbonding whose unbonding completion occurs at the given height and time.
func (k Keeper) ValidatorQueueIterator(ctx context.Context, endTime time.Time, endHeight int64) (corestore.Iterator, error) {
	store := k.storeService.OpenKVStore(ctx)
	return store.Iterator(types.ValidatorQueueKey, storetypes.InclusiveEndBytes(types.GetValidatorQueueKey(endTime, endHeight)))
}

// UnbondAllMatureValidators unbonds all the mature unbonding validators that
// have finished their unbonding period.
func (k Keeper) UnbondAllMatureValidators(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	blockTime := sdkCtx.BlockTime()
	blockHeight := sdkCtx.BlockHeight()

	k.Logger(ctx).Info("UnbondAllMatureValidators: starting EndBlock processing",
		"block_time", blockTime,
		"block_height", blockHeight)

	unbondingValidators, err := k.GetPendingUnbondingValidators(ctx, blockTime, blockHeight)
	if err != nil {
		k.Logger(ctx).Error("UnbondAllMatureValidators: failed to get pending validators",
			"error", err)
		return err
	}

	k.Logger(ctx).Info("UnbondAllMatureValidators: retrieved pending validators",
		"total_queue_entries", len(unbondingValidators))

	keys := make([]string, 0, len(unbondingValidators))
	for k := range unbondingValidators {
		keys = append(keys, k)
	}

	types.SortValidatorQueueKeysByAscendingTimestampOrder(keys)

	k.Logger(ctx).Info("UnbondAllMatureValidators: sorted queue keys",
		"sorted_keys", keys)

	processedCount := 0
	skippedCount := 0

	for _, key := range keys {
		time, height, err := types.ParseCacheValidatorQueueKey(key)
		if err != nil {
			k.Logger(ctx).Error("UnbondAllMatureValidators: failed to parse key",
				"error", err,
				"key", key)
			return fmt.Errorf("failed to parse unbonding key: %w", err)
		}

		k.Logger(ctx).Info("UnbondAllMatureValidators: processing queue entry",
			"key", key,
			"time", time,
			"height", height,
			"validators_count", len(unbondingValidators[key]),
			"validators", unbondingValidators[key])

		if nonMature := time.After(blockTime); nonMature {
			k.Logger(ctx).Info("UnbondAllMatureValidators: reached non-mature validators, stopping",
				"unbonding_time", time,
				"block_time", blockTime,
				"skipped_entries", len(keys)-processedCount-skippedCount)
			return nil
		}

		// All addresses for the given key have the same unbonding height and time.
		// We only unbond if the height and time are less than the current height
		// and time.
		if height <= blockHeight && (time.Before(blockTime) || time.Equal(blockTime)) {
			k.Logger(ctx).Info("UnbondAllMatureValidators: queue entry is mature, processing validators",
				"time", time,
				"height", height,
				"validator_count", len(unbondingValidators[key]))

			for _, valAddr := range unbondingValidators[key] {
				k.Logger(ctx).Info("UnbondAllMatureValidators: processing validator",
					"validator", valAddr,
					"time", time,
					"height", height)

				addr, err := k.validatorAddressCodec.StringToBytes(valAddr)
				if err != nil {
					k.Logger(ctx).Error("UnbondAllMatureValidators: failed to parse validator address",
						"error", err,
						"validator", valAddr)
					return err
				}
				val, err := k.GetValidator(ctx, addr)
				if err != nil {
					k.Logger(ctx).Error("UnbondAllMatureValidators: validator not found",
						"error", err,
						"validator", valAddr)
					return errorsmod.Wrap(err, "validator in the unbonding queue was not found")
				}

				k.Logger(ctx).Info("UnbondAllMatureValidators: validator details",
					"validator", valAddr,
					"status", val.Status.String(),
					"jailed", val.Jailed,
					"tokens", val.Tokens.String(),
					"delegator_shares", val.DelegatorShares.String())

				if !val.IsUnbonding() {
					k.Logger(ctx).Error("UnbondAllMatureValidators: validator not in unbonding state",
						"validator", valAddr,
						"status", val.Status.String())
					return fmt.Errorf("unexpected validator in unbonding queue; status was not unbonding")
				}

				val, err = k.UnbondingToUnbonded(ctx, val)
				if err != nil {
					k.Logger(ctx).Error("UnbondAllMatureValidators: failed to transition to unbonded",
						"error", err,
						"validator", valAddr)
					return err
				}

				k.Logger(ctx).Info("UnbondAllMatureValidators: validator transitioned to unbonded",
					"validator", valAddr,
					"delegator_shares", val.DelegatorShares.String())

				if val.GetDelegatorShares().IsZero() {
					k.Logger(ctx).Info("UnbondAllMatureValidators: validator has zero shares, removing",
						"validator", valAddr)

					str, err := k.validatorAddressCodec.StringToBytes(val.GetOperator())
					if err != nil {
						return err
					}
					if err = k.RemoveValidator(ctx, str); err != nil {
						k.Logger(ctx).Error("UnbondAllMatureValidators: failed to remove validator",
							"error", err,
							"validator", valAddr)
						return err
					}

					k.Logger(ctx).Info("UnbondAllMatureValidators: validator removed",
						"validator", valAddr)
				}

				// remove validator from queue
				if err = k.DeleteValidatorQueue(ctx, val); err != nil {
					k.Logger(ctx).Error("UnbondAllMatureValidators: failed to delete from queue",
						"error", err,
						"validator", valAddr)
					return err
				}

				processedCount++
				k.Logger(ctx).Info("UnbondAllMatureValidators: successfully processed validator",
					"validator", valAddr,
					"total_processed", processedCount)
			}
		} else {
			k.Logger(ctx).Info("UnbondAllMatureValidators: skipping non-mature entry",
				"time", time,
				"height", height,
				"block_time", blockTime,
				"block_height", blockHeight)
			skippedCount++
		}
	}

	k.Logger(ctx).Info("UnbondAllMatureValidators: completed EndBlock processing",
		"total_processed", processedCount,
		"total_skipped", skippedCount)

	return nil
}

// IsValidatorJailed checks and returns boolean of a validator status jailed or not.
func (k Keeper) IsValidatorJailed(ctx context.Context, addr sdk.ConsAddress) (bool, error) {
	v, err := k.GetValidatorByConsAddr(ctx, addr)
	if err != nil {
		return false, err
	}

	return v.Jailed, nil
}

// GetPubKeyByConsAddr returns the consensus public key by consensus address.
func (k Keeper) GetPubKeyByConsAddr(ctx context.Context, addr sdk.ConsAddress) (cmtprotocrypto.PublicKey, error) {
	v, err := k.GetValidatorByConsAddr(ctx, addr)
	if err != nil {
		return cmtprotocrypto.PublicKey{}, err
	}

	pubkey, err := v.CmtConsPublicKey()
	if err != nil {
		return cmtprotocrypto.PublicKey{}, err
	}

	return pubkey, nil
}

// GetPendingUnbondingValidators gets unbonding validators from the cache or the store
func (k Keeper) GetPendingUnbondingValidators(ctx context.Context, endTime time.Time, endHeight int64) (map[string][]string, error) {
	if k.cache != nil {
		addrs, err := k.cache.GetUnbondingValidatorsQueue(ctx)
		if err == nil {
			k.Logger(ctx).Info("GetPendingUnbondingValidators: retrieved from cache",
				"total_keys", len(addrs),
				"cache_contents", addrs)
			return addrs, nil
		}
		k.Logger(ctx).Error("GetPendingUnbondingValidators from cache failed. Error: %s", err)
	}

	k.Logger(ctx).Info("GetPendingUnbondingValidators: falling back to store",
		"end_time", endTime,
		"end_height", endHeight)

	storeAddrs, err := k.GetUnbondingValidatorsFromStore(ctx, endTime, endHeight)
	if err == nil {
		k.Logger(ctx).Info("GetPendingUnbondingValidators: retrieved from store",
			"total_keys", len(storeAddrs),
			"store_contents", storeAddrs)
	}
	return storeAddrs, err
}

// GetUnbondingValidatorsFromStore gets unbonding validators from the store for a given height and time.
func (k Keeper) GetUnbondingValidatorsFromStore(ctx context.Context, endTime time.Time, endHeight int64) (map[string][]string, error) {
	iterator, err := k.ValidatorQueueIterator(ctx, endTime, endHeight)
	if err != nil {
		return nil, err
	}
	defer iterator.Close()
	unbondingValidators, err := k.getUnbondingValidatorsFromIterator(iterator)
	if err != nil {
		return nil, err
	}

	return unbondingValidators, nil
}

// GetAllUnbondingValidatorsFromStore gets unbonding validators from the store
func (k Keeper) GetAllUnbondingValidatorsFromStore(ctx context.Context) (map[string][]string, error) {
	iterator, err := k.ValidatorQueueIteratorAll(ctx)
	if err != nil {
		return nil, err
	}
	defer iterator.Close()
	unbondingValidators, err := k.getUnbondingValidatorsFromIterator(iterator)
	if err != nil {
		return nil, err
	}

	return unbondingValidators, nil
}

// getUnbondingValidatorsFromIterator gets unbonding validators from the iterator.
func (k Keeper) getUnbondingValidatorsFromIterator(iterator corestore.Iterator) (map[string][]string, error) {
	unbondingValidators := make(map[string][]string)

	for ; iterator.Valid(); iterator.Next() {
		key := iterator.Key()
		keyTime, keyHeight, err := types.ParseValidatorQueueKey(key)
		if err != nil {
			return nil, fmt.Errorf("failed to parse unbonding key: %w", err)
		}

		addrs := types.ValAddresses{}
		if err = k.cdc.Unmarshal(iterator.Value(), &addrs); err != nil {
			return nil, err
		}

		unbondingValidators[types.GetCacheValidatorQueueKey(keyTime, keyHeight)] = addrs.Addresses
	}

	return unbondingValidators, nil
}
