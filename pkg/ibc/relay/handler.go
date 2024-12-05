package relay

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/telemetry"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v8/modules/core/02-client/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sync/errgroup"
)

const (
	maxRetries        = 5
	baseRetryDelay    = 500 * time.Millisecond
	maxRetryDelay     = 10 * time.Second
	maxBlockTimeDrift = 30 * time.Second
)

// BlockTimeMonitor tracks block production rates for both chains
type BlockTimeMonitor struct {
	mu            sync.RWMutex
	blockTimes    map[string][]time.Time // Last N block times per chain
	avgBlockTimes map[string]time.Duration
}

func NewBlockTimeMonitor() *BlockTimeMonitor {
	return &BlockTimeMonitor{
		blockTimes:    make(map[string][]time.Time),
		avgBlockTimes: make(map[string]time.Duration),
	}
}

func (m *BlockTimeMonitor) AddBlockTime(chainID string, blockTime time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Keep last 100 block times
	if len(m.blockTimes[chainID]) >= 100 {
		m.blockTimes[chainID] = m.blockTimes[chainID][1:]
	}
	m.blockTimes[chainID] = append(m.blockTimes[chainID], blockTime)

	// Recalculate average block time
	if len(m.blockTimes[chainID]) > 1 {
		var totalDuration time.Duration
		for i := 1; i < len(m.blockTimes[chainID]); i++ {
			totalDuration += m.blockTimes[chainID][i].Sub(m.blockTimes[chainID][i-1])
		}
		m.avgBlockTimes[chainID] = totalDuration / time.Duration(len(m.blockTimes[chainID])-1)
	}
}

func (m *BlockTimeMonitor) GetAverageBlockTime(chainID string) time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.avgBlockTimes[chainID]
}

// RelayMetrics tracks relay performance metrics
type RelayMetrics struct {
	PacketsRelayed *prometheus.CounterVec
	PacketErrors   *prometheus.CounterVec
	RelayLatency   *prometheus.HistogramVec
	RetryCount     *prometheus.HistogramVec
	BlockTimeDrift *prometheus.GaugeVec
}

type PacketInfo struct {
	Sequence         uint64
	SourcePort       string
	SourceChan       string
	SourceChainID    string
	DestPort         string
	DestChan         string
	DestChainID      string
	Data             []byte
	TimeoutHeight    clienttypes.Height
	TimeoutTimestamp uint64
	RetryCount       int
	FirstAttempt     time.Time
	Status           PacketStatus
	LastRetry        time.Time
}

type PacketStatus int

const (
	PacketPending PacketStatus = iota
	PacketSent
	PacketAcked
	PacketTimedOut
)

type RelayHandler struct {
	mu sync.RWMutex

	minTimeout time.Duration

	pendingPackets map[string][]PacketInfo // key: channelID
	acks           map[string][]uint64     // key: channelID
	timeouts       map[string][]uint64     // key: channelID
	blockMonitor   *BlockTimeMonitor
	metrics        *RelayMetrics
	ctx            context.Context
	cancel         context.CancelFunc

	// Add this field for testing
	relayPacketFn func(ctx sdk.Context, packet PacketInfo, timeout time.Duration) error
}

func NewRelayHandler(minTimeout time.Duration) *RelayHandler {
	ctx, cancel := context.WithCancel(context.Background())
	h := &RelayHandler{
		pendingPackets: make(map[string][]PacketInfo),
		acks:           make(map[string][]uint64),
		timeouts:       make(map[string][]uint64),
		blockMonitor:   NewBlockTimeMonitor(),
		metrics:        initMetrics(),
		ctx:            ctx,
		cancel:         cancel,
		minTimeout:     minTimeout,
	}
	h.relayPacketFn = h.relayPacket // default to real implementation
	return h
}

func (h *RelayHandler) validatePacket(packet PacketInfo) error {
	// Validate required fields
	if packet.Sequence == 0 {
		return fmt.Errorf("invalid sequence: sequence number must be greater than 0")
	}
	if packet.SourcePort == "" {
		return fmt.Errorf("missing fields: source port is required")
	}
	if packet.SourceChan == "" {
		return fmt.Errorf("missing fields: source channel is required")
	}
	if packet.SourceChainID == "" {
		return fmt.Errorf("missing fields: source chain ID is required")
	}
	if packet.DestPort == "" {
		return fmt.Errorf("missing fields: destination port is required")
	}
	if packet.DestChan == "" {
		return fmt.Errorf("missing fields: destination channel is required")
	}
	if packet.DestChainID == "" {
		return fmt.Errorf("missing fields: destination chain ID is required")
	}
	if packet.Data == nil {
		return fmt.Errorf("missing fields: packet data is required")
	}
	return nil
}

func (h *RelayHandler) QueuePacket(ctx sdk.Context, packet PacketInfo) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err := h.validatePacket(packet); err != nil {
		return err
	}

	// Check for timeout before queuing
	if h.isPacketTimedOut(ctx, packet) {
		return fmt.Errorf("packet timeout: sequence %d", packet.Sequence)
	}

	channelID := fmt.Sprintf("%s/%s", packet.SourcePort, packet.SourceChan)

	// Validate sequence ordering
	if packets, exists := h.pendingPackets[channelID]; exists {
		// Check for duplicate packets
		for _, p := range packets {
			if p.Sequence == packet.Sequence {
				return fmt.Errorf("duplicate packet sequence %d", packet.Sequence)
			}
		}

		// Ensure packets are ordered
		if len(packets) > 0 && packet.Sequence <= packets[len(packets)-1].Sequence {
			return fmt.Errorf("out of order packet: got %d, expected > %d",
				packet.Sequence, packets[len(packets)-1].Sequence)
		}
	}

	packet.Status = PacketPending
	packet.FirstAttempt = ctx.BlockTime()
	h.pendingPackets[channelID] = append(h.pendingPackets[channelID], packet)
	return nil
}

func (h *RelayHandler) ProcessPackets(ctx sdk.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	stdCtx := ctx.Context() // Get the standard context
	g, gCtx := errgroup.WithContext(stdCtx)

	for channelID, packets := range h.pendingPackets {
		channelID := channelID
		packets := packets

		g.Go(func() error {
			sdkCtx := ctx.WithContext(gCtx) // Create new SDK context with errgroup context
			return h.processChannelPackets(sdkCtx, channelID, packets)
		})
	}

	return g.Wait()
}

func (h *RelayHandler) processChannelPackets(ctx sdk.Context, channelID string, packets []PacketInfo) error {
	var remainingPackets []PacketInfo

	// Sort packets by sequence number
	sort.Slice(packets, func(i, j int) bool {
		return packets[i].Sequence < packets[j].Sequence
	})

	for _, packet := range packets {
		// Check if context is cancelled
		select {
		case <-ctx.Context().Done():
			return ctx.Context().Err()
		default:
		}

		// Check for timeout based on block times
		if h.isPacketTimedOut(ctx, packet) {
			h.timeouts[channelID] = append(h.timeouts[channelID], packet.Sequence)
			h.metrics.PacketErrors.WithLabelValues(
				packet.SourceChainID,
				packet.DestChainID,
				"timeout",
			).Inc()
			continue
		}

		// Get current block times and check drift
		sourceAvgBlockTime := h.blockMonitor.GetAverageBlockTime(packet.SourceChainID)
		destAvgBlockTime := h.blockMonitor.GetAverageBlockTime(packet.DestChainID)

		drift := math.Abs(float64(sourceAvgBlockTime - destAvgBlockTime))
		h.metrics.BlockTimeDrift.WithLabelValues(packet.SourceChainID, packet.DestChainID).Set(drift)

		if drift > float64(maxBlockTimeDrift) {
			// Log warning but continue processing
			telemetry.IncrCounter(1, "ibc", "block_time_drift_exceeded")
		}

		// Calculate adaptive timeout based on block times
		adaptiveTimeout := h.calculateAdaptiveTimeout(sourceAvgBlockTime, destAvgBlockTime)

		// Attempt to relay with retry logic
		err := h.relayWithRetry(ctx, packet, adaptiveTimeout)
		if err != nil {
			if packet.RetryCount >= maxRetries {
				h.timeouts[channelID] = append(h.timeouts[channelID], packet.Sequence)
				h.metrics.PacketErrors.WithLabelValues(packet.SourceChainID, packet.DestChainID, "max_retries").Inc()
				continue
			}
			packet.RetryCount++
			remainingPackets = append(remainingPackets, packet)
			continue
		}

		// Success - record metrics
		h.acks[channelID] = append(h.acks[channelID], packet.Sequence)
		latency := time.Since(packet.FirstAttempt)
		h.metrics.PacketsRelayed.WithLabelValues(packet.SourceChainID, packet.DestChainID).Inc()
		h.metrics.RelayLatency.WithLabelValues(packet.SourceChainID, packet.DestChainID).Observe(latency.Seconds())
		h.metrics.RetryCount.WithLabelValues(packet.SourceChainID, packet.DestChainID).Observe(float64(packet.RetryCount))
	}

	if len(remainingPackets) > 0 {
		h.pendingPackets[channelID] = remainingPackets
	} else {
		delete(h.pendingPackets, channelID)
	}

	return nil
}

func (h *RelayHandler) relayWithRetry(ctx sdk.Context, packet PacketInfo, timeout time.Duration) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Context().Done():
			return ctx.Context().Err()
		default:
		}

		// Use the function field instead of direct call
		if err := h.relayPacketFn(ctx, packet, timeout); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

func (h *RelayHandler) relayPacket(ctx sdk.Context, packet PacketInfo, timeout time.Duration) error {
	// Create context with timeout
	timeoutCtx, cancel := context.WithTimeout(ctx.Context(), timeout)
	defer cancel()

	// Generate proof at current height
	proofHeight := clienttypes.NewHeight(0, uint64(ctx.BlockHeight()))

	proof, err := h.queryPacketCommitmentProof(timeoutCtx, packet, proofHeight)
	if err != nil {
		return fmt.Errorf("failed to query proof: %w", err)
	}

	// Construct IBC packet
	ibcPacket := channeltypes.Packet{
		Sequence:           packet.Sequence,
		SourcePort:         packet.SourcePort,
		SourceChannel:      packet.SourceChan,
		DestinationPort:    packet.DestPort,
		DestinationChannel: packet.DestChan,
		Data:               packet.Data,
		TimeoutHeight:      packet.TimeoutHeight,
		TimeoutTimestamp:   packet.TimeoutTimestamp,
	}

	// In real implementation: Submit MsgRecvPacket to destination chain
	if err := h.submitRecvPacket(timeoutCtx, ibcPacket, proof, proofHeight); err != nil {
		return fmt.Errorf("failed to submit recv packet: %w", err)
	}

	return nil
}

func (h *RelayHandler) calculateAdaptiveTimeout(sourceBlockTime, destBlockTime time.Duration) time.Duration {
	// Base timeout starts with slower chain's block time
	baseTimeout := sourceBlockTime
	if destBlockTime > sourceBlockTime {
		baseTimeout = destBlockTime
	}

	// Add buffer based on block time difference
	buffer := time.Duration(math.Abs(float64(sourceBlockTime-destBlockTime))) * 3

	return baseTimeout + buffer + h.minTimeout
}

// Mock implementations - in real code these would interact with actual chains
func (h *RelayHandler) queryPacketCommitmentProof(_ context.Context, _ PacketInfo, _ clienttypes.Height) ([]byte, error) {
	return []byte("mock proof"), nil
}

func (h *RelayHandler) submitRecvPacket(_ context.Context, _ channeltypes.Packet, _ []byte, _ clienttypes.Height) error {
	return nil
}

func (h *RelayHandler) isPacketTimedOut(ctx sdk.Context, packet PacketInfo) bool {
	if packet.TimeoutHeight.IsZero() && packet.TimeoutTimestamp == 0 {
		return false
	}

	if !packet.TimeoutHeight.IsZero() {
		if ctx.BlockHeight() >= int64(packet.TimeoutHeight.RevisionHeight) {
			return true
		}
	}

	if packet.TimeoutTimestamp != 0 {
		currentTime := ctx.BlockTime().UnixNano()
		if currentTime >= int64(packet.TimeoutTimestamp) {
			return true
		}
	}

	return false
}

func initMetrics() *RelayMetrics {
	return &RelayMetrics{
		PacketsRelayed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "ibc",
				Name:      "packets_relayed_total",
				Help:      "Total number of IBC packets successfully relayed",
			},
			[]string{"source_chain", "dest_chain"},
		),
		PacketErrors: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: "ibc",
				Name:      "packet_errors_total",
				Help:      "Total number of IBC packet relay errors",
			},
			[]string{"source_chain", "dest_chain", "error_type"},
		),
		RelayLatency: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "ibc",
				Name:      "relay_latency_seconds",
				Help:      "Time taken to relay packets",
				Buckets:   prometheus.ExponentialBuckets(0.1, 2, 10),
			},
			[]string{"source_chain", "dest_chain"},
		),
		RetryCount: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: "ibc",
				Name:      "retry_count",
				Help:      "Number of retries needed for successful relay",
				Buckets:   []float64{0, 1, 2, 3, 4, 5},
			},
			[]string{"source_chain", "dest_chain"},
		),
		BlockTimeDrift: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: "ibc",
				Name:      "block_time_drift_seconds",
				Help:      "Difference in block times between chains",
			},
			[]string{"source_chain", "dest_chain"},
		),
	}
}
