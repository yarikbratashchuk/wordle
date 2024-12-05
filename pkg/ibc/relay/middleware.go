package relay

import (
	sdk "github.com/cosmos/cosmos-sdk/types"
	capabilitytypes "github.com/cosmos/ibc-go/modules/capability/types"
	channeltypes "github.com/cosmos/ibc-go/v8/modules/core/04-channel/types"
	ibcexported "github.com/cosmos/ibc-go/v8/modules/core/exported"
)

// Middleware implements the IBC middleware interface
type Middleware struct {
	relay *RelayHandler
}

func NewRelayMiddleware(relay *RelayHandler) Middleware {
	return Middleware{
		relay: relay,
	}
}

// OnRecvPacket implements the IBC middleware interface
func (im Middleware) OnRecvPacket(
	ctx sdk.Context,
	packet channeltypes.Packet,
	relayer sdk.AccAddress,
) ibcexported.Acknowledgement {
	packetInfo := PacketInfo{
		Sequence:         packet.Sequence,
		SourcePort:       packet.SourcePort,
		SourceChan:       packet.SourceChannel,
		DestPort:         packet.DestinationPort,
		DestChan:         packet.DestinationChannel,
		Data:             packet.Data,
		TimeoutHeight:    packet.TimeoutHeight,
		TimeoutTimestamp: packet.TimeoutTimestamp,
	}

	if err := im.relay.QueuePacket(ctx, packetInfo); err != nil {
		return channeltypes.NewErrorAcknowledgement(err)
	}

	if err := im.relay.ProcessPackets(ctx); err != nil {
		return channeltypes.NewErrorAcknowledgement(err)
	}

	// Return successful acknowledgement
	return channeltypes.NewResultAcknowledgement([]byte{byte(1)})
}

func (im Middleware) OnAcknowledgementPacket(ctx sdk.Context, packet channeltypes.Packet, acknowledgement []byte, relayer sdk.AccAddress) error {
	// Handle acknowledgements if needed
	return nil
}

func (im Middleware) OnTimeoutPacket(ctx sdk.Context, packet channeltypes.Packet, relayer sdk.AccAddress) error {
	// Handle timeouts if needed
	return nil
}

func (im Middleware) SendPacket(ctx sdk.Context, chanCap *capabilitytypes.Capability, packet ibcexported.PacketI) error {
	return nil
}

func (im Middleware) WriteAcknowledgement(ctx sdk.Context, chanCap *capabilitytypes.Capability, packet ibcexported.PacketI, ack ibcexported.Acknowledgement) error {
	return nil
}

func (im Middleware) GetAppVersion(ctx sdk.Context, portID, channelID string) (string, bool) {
	return "", false
}

func (im Middleware) OnChanCloseInit(ctx sdk.Context, portID, channelID string) error {
	return nil
}

func (im Middleware) OnChanCloseConfirm(ctx sdk.Context, portID, channelID string) error {
	return nil
}

func (im Middleware) OnChanOpenInit(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID string,
	channelID string,
	chanCap *capabilitytypes.Capability,
	counterparty channeltypes.Counterparty,
	version string,
) (string, error) {
	return version, nil
}

func (im Middleware) OnChanOpenTry(
	ctx sdk.Context,
	order channeltypes.Order,
	connectionHops []string,
	portID,
	channelID string,
	chanCap *capabilitytypes.Capability,
	counterparty channeltypes.Counterparty,
	counterpartyVersion string,
) (string, error) {
	return counterpartyVersion, nil
}

func (im Middleware) OnChanOpenAck(
	ctx sdk.Context,
	portID,
	channelID string,
	counterpartyChannelID string,
	counterpartyVersion string,
) error {
	return nil
}

func (im Middleware) OnChanOpenConfirm(
	ctx sdk.Context,
	portID,
	channelID string,
) error {
	return nil
}
