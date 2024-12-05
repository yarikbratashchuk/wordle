package types

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
)

const (
	// ModuleName defines the module name
	ModuleName = "wordle"

	// StoreKey defines the primary module store key
	StoreKey = ModuleName

	// MemStoreKey defines the in-memory store key
	MemStoreKey = "mem_wordle"

	// Additional store prefixes
	BlockStatePrefix    = "block_state"
	ConsensusInfoPrefix = "consensus_info"
	StateChangePrefix   = "state_change"
)

var (
	ParamsKey = []byte("p_wordle")
)

func KeyPrefix(p string) []byte {
	return []byte(p)
}

// BlockStateKey returns the store key to retrieve block state
func BlockStateKey(height int64) []byte {
	return append(KeyPrefix(BlockStatePrefix), sdk.Uint64ToBigEndian(uint64(height))...)
}

// ConsensusInfoKey returns the store key to retrieve consensus info
func ConsensusInfoKey(height int64) []byte {
	return append(KeyPrefix(ConsensusInfoPrefix), sdk.Uint64ToBigEndian(uint64(height))...)
}
