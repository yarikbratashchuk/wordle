package relay

import (
	"context"
	"fmt"
	"testing"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	"github.com/stretchr/testify/require"
)

const minTimeout = 6 * time.Second

func TestPacketOrdering(t *testing.T) {
	handler := NewRelayHandler(minTimeout)
	ctx := sdk.Context{}.WithBlockTime(time.Now())

	// Test sequential packet ordering
	packets := []PacketInfo{
		{
			Sequence:         1,
			SourcePort:       "transfer",
			SourceChan:       "channel-0",
			SourceChainID:    "chain-a",
			DestPort:         "transfer",
			DestChan:         "channel-1",
			DestChainID:      "chain-b",
			Data:             []byte("test data"),
			TimeoutHeight:    clienttypes.Height{},
			TimeoutTimestamp: 0,
			RetryCount:       0,
			FirstAttempt:     time.Now(),
			Status:           PacketPending,
			LastRetry:        time.Time{},
		},
		{
			Sequence:         2,
			SourcePort:       "transfer",
			SourceChan:       "channel-0",
			SourceChainID:    "chain-a",
			DestPort:         "transfer",
			DestChan:         "channel-1",
			DestChainID:      "chain-b",
			Data:             []byte("test data"),
			TimeoutHeight:    clienttypes.Height{},
			TimeoutTimestamp: 0,
			RetryCount:       0,
			FirstAttempt:     time.Now(),
			Status:           PacketPending,
			LastRetry:        time.Time{},
		},
		{
			Sequence:         3,
			SourcePort:       "transfer",
			SourceChan:       "channel-0",
			SourceChainID:    "chain-a",
			DestPort:         "transfer",
			DestChan:         "channel-1",
			DestChainID:      "chain-b",
			Data:             []byte("test data"),
			TimeoutHeight:    clienttypes.Height{},
			TimeoutTimestamp: 0,
			RetryCount:       0,
			FirstAttempt:     time.Now(),
			Status:           PacketPending,
			LastRetry:        time.Time{},
		},
	}

	for _, p := range packets {
		err := handler.QueuePacket(ctx, p)
		require.NoError(t, err)
	}

	// Test out of order packet
	outOfOrder := PacketInfo{
		Sequence:         1,
		SourcePort:       "transfer",
		SourceChan:       "channel-0",
		SourceChainID:    "chain-a",
		DestPort:         "transfer",
		DestChan:         "channel-1",
		DestChainID:      "chain-b",
		Data:             []byte("test data"),
		TimeoutHeight:    clienttypes.Height{},
		TimeoutTimestamp: 0,
		RetryCount:       0,
		FirstAttempt:     time.Now(),
		Status:           PacketPending,
		LastRetry:        time.Time{},
	}
	err := handler.QueuePacket(ctx, outOfOrder)
	require.Error(t, err)

	// Test duplicate packet
	duplicatePacket := PacketInfo{
		Sequence:         2,
		SourceChainID:    "chain-a",
		DestChainID:      "chain-b",
		SourcePort:       "transfer",
		SourceChan:       "channel-0",
		DestPort:         "transfer",
		DestChan:         "channel-1",
		Data:             []byte("test data"),
		TimeoutHeight:    clienttypes.Height{},
		TimeoutTimestamp: 0,
		RetryCount:       0,
		FirstAttempt:     time.Now(),
		Status:           PacketPending,
		LastRetry:        time.Time{},
	}
	err = handler.QueuePacket(ctx, duplicatePacket)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate packet")

	// Test packet with invalid sequence
	invalidSequencePacket := PacketInfo{
		Sequence:         0,
		SourcePort:       "transfer",
		SourceChan:       "channel-0",
		SourceChainID:    "chain-a",
		DestPort:         "transfer",
		DestChan:         "channel-1",
		DestChainID:      "chain-b",
		Data:             []byte("test data"),
		TimeoutHeight:    clienttypes.Height{},
		TimeoutTimestamp: 0,
		RetryCount:       0,
		FirstAttempt:     time.Now(),
		Status:           PacketPending,
		LastRetry:        time.Time{},
	}
	err = handler.QueuePacket(ctx, invalidSequencePacket)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid sequence")

	// Test packet with missing fields
	missingFieldsPacket := PacketInfo{
		Sequence:         4,
		SourcePort:       "",
		SourceChan:       "",
		SourceChainID:    "",
		DestPort:         "",
		DestChan:         "",
		DestChainID:      "",
		Data:             nil,
		TimeoutHeight:    clienttypes.Height{},
		TimeoutTimestamp: 0,
		RetryCount:       0,
		FirstAttempt:     time.Now(),
		Status:           PacketPending,
		LastRetry:        time.Time{},
	}
	err = handler.QueuePacket(ctx, missingFieldsPacket)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing fields")
}

func TestBlockTimeDrift(t *testing.T) {
	handler := NewRelayHandler(minTimeout)
	now := time.Now()

	testCases := []struct {
		name            string
		chainABlockTime time.Duration
		chainBBlockTime time.Duration
		expectedTimeout time.Duration
	}{
		{
			name:            "similar block times",
			chainABlockTime: 5 * time.Second,
			chainBBlockTime: 6 * time.Second,
			expectedTimeout: 15 * time.Second, // minimum timeout for similar speeds
		},
		{
			name:            "significant drift",
			chainABlockTime: 5 * time.Second,
			chainBBlockTime: 15 * time.Second,
			expectedTimeout: 45 * time.Second, // should be larger due to drift
		},
		{
			name:            "extreme drift",
			chainABlockTime: 3 * time.Second,
			chainBBlockTime: 30 * time.Second,
			expectedTimeout: 90 * time.Second, // should handle extreme differences
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset block monitor for each test case
			handler.blockMonitor = NewBlockTimeMonitor()

			// Simulate Chain A blocks
			for i := 0; i < 10; i++ {
				handler.blockMonitor.AddBlockTime("chain-a",
					now.Add(time.Duration(i)*tc.chainABlockTime))
			}

			// Simulate Chain B blocks
			for i := 0; i < 10; i++ {
				handler.blockMonitor.AddBlockTime("chain-b",
					now.Add(time.Duration(i)*tc.chainBBlockTime))
			}

			timeout := handler.calculateAdaptiveTimeout(
				handler.blockMonitor.GetAverageBlockTime("chain-a"),
				handler.blockMonitor.GetAverageBlockTime("chain-b"),
			)

			require.GreaterOrEqual(t, int64(timeout), int64(tc.expectedTimeout))
		})
	}
}

func TestPacketDelayHandling(t *testing.T) {
	handler := NewRelayHandler(minTimeout)
	now := time.Now()

	testCases := []struct {
		name        string
		packet      PacketInfo
		delayTime   time.Duration
		shouldQueue bool
	}{
		{
			name: "normal packet",
			packet: PacketInfo{
				Sequence:         1,
				SourcePort:       "transfer",
				SourceChan:       "channel-0",
				SourceChainID:    "chain-a",
				DestPort:         "transfer",
				DestChan:         "channel-1",
				DestChainID:      "chain-b",
				Data:             []byte("test data"),
				TimeoutHeight:    clienttypes.Height{RevisionHeight: 100},
				TimeoutTimestamp: 0,
				RetryCount:       0,
				FirstAttempt:     now,
				Status:           PacketPending,
				LastRetry:        time.Time{},
			},
			delayTime:   time.Second,
			shouldQueue: true,
		},
		{
			name: "delayed packet near timeout",
			packet: PacketInfo{
				Sequence:         2,
				SourcePort:       "transfer",
				SourceChan:       "channel-0",
				SourceChainID:    "chain-a",
				DestPort:         "transfer",
				DestChan:         "channel-1",
				DestChainID:      "chain-b",
				Data:             []byte("test data"),
				TimeoutHeight:    clienttypes.Height{RevisionHeight: 10},
				TimeoutTimestamp: uint64(now.Add(5 * time.Second).UnixNano()),
				RetryCount:       0,
				FirstAttempt:     now,
				Status:           PacketPending,
				LastRetry:        time.Time{},
			},
			delayTime:   10 * time.Second,
			shouldQueue: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := sdk.Context{}.WithBlockTime(now.Add(tc.delayTime))

			err := handler.QueuePacket(ctx, tc.packet)
			if tc.shouldQueue {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), "packet timeout")
			}
		})
	}
}

func TestPacketRetries(t *testing.T) {
	handler := NewRelayHandler(minTimeout)

	// Override relayPacket to always fail
	handler.relayPacketFn = func(ctx sdk.Context, packet PacketInfo, timeout time.Duration) error {
		return fmt.Errorf("mock relay error")
	}

	packet := PacketInfo{
		Sequence:         1,
		SourcePort:       "transfer",
		SourceChan:       "channel-0",
		SourceChainID:    "chain-a",
		DestPort:         "transfer",
		DestChan:         "channel-1",
		DestChainID:      "chain-b",
		Data:             []byte("test data"),
		TimeoutHeight:    clienttypes.Height{},
		TimeoutTimestamp: 0,
		RetryCount:       0,
		FirstAttempt:     time.Now(),
		Status:           PacketPending,
		LastRetry:        time.Time{},
	}

	// Initialize a valid context
	ctx := sdk.Context{}.WithContext(context.Background())

	// Test retry logic
	err := handler.relayWithRetry(ctx, packet, time.Second)
	require.Error(t, err)
	require.Contains(t, err.Error(), "max retries exceeded")
}
