package keeper

import (
	"context"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"wordle/x/wordle/types"
)

// SaveBlockState stores the current block state
func (k Keeper) SaveBlockState(ctx context.Context, state types.BlockState) error {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	bz, err := k.cdc.Marshal(&state)
	if err != nil {
		return err
	}
	store.Set(types.BlockStateKey(state.Height), bz)
	return nil
}

// GetBlockState retrieves the block state for a given height
func (k Keeper) GetBlockState(ctx context.Context, height int64) (types.BlockState, error) {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	var state types.BlockState
	bz := store.Get(types.BlockStateKey(height))
	if bz == nil {
		return state, nil
	}
	err := k.cdc.Unmarshal(bz, &state)
	return state, err
}

// SaveConsensusInfo stores consensus information
func (k Keeper) SaveConsensusInfo(ctx context.Context, info types.ConsensusInfo) error {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	bz, err := k.cdc.Marshal(&info)
	if err != nil {
		return err
	}
	store.Set(types.ConsensusInfoKey(info.Height), bz)
	return nil
}

// GetConsensusInfo retrieves consensus information
func (k Keeper) GetConsensusInfo(ctx context.Context, height int64) (types.ConsensusInfo, error) {
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	var info types.ConsensusInfo
	bz := store.Get(types.ConsensusInfoKey(height))
	if bz == nil {
		return info, nil
	}
	err := k.cdc.Unmarshal(bz, &info)
	return info, err
}

// TrackStateChange records a state change for potential rollback
func (k Keeper) TrackStateChange(ctx context.Context, key, value []byte, isDeleted bool) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	state, _ := k.GetBlockState(ctx, height)
	state.Changes = append(state.Changes, &types.StateChange{
		Key:       key,
		Value:     value,
		IsDeleted: isDeleted,
	})
	k.SaveBlockState(ctx, state)
}
