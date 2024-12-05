package keeper

import (
	"context"
	"wordle/x/wordle/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (k Keeper) CheckConsensus(ctx context.Context) bool {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	info, err := k.GetConsensusInfo(ctx, height)
	if err != nil {
		k.Logger().Error("failed to get consensus info", "error", err)
		return false
	}

	return info.HasConsensus()
}

func (k Keeper) RollbackState(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	state, err := k.GetBlockState(ctx, height)
	if err != nil {
		return err
	}

	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))

	// Reverse apply changes
	for i := len(state.Changes) - 1; i >= 0; i-- {
		change := state.Changes[i]
		if change.IsDeleted {
			store.Set(change.Key, change.Value) // Restore deleted value
		} else {
			store.Delete(change.Key) // Remove added value
		}
	}

	return nil
}

func (k Keeper) CommitState(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	height := sdkCtx.BlockHeight()

	// Clear temporary state tracking
	store := runtime.KVStoreAdapter(k.storeService.OpenKVStore(ctx))
	store.Delete(types.BlockStateKey(height))
	store.Delete(types.ConsensusInfoKey(height))

	return nil
}
