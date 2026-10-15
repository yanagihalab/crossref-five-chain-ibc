package keeper

import (
	"fmt"

	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"
	ibckeeper "github.com/cosmos/ibc-go/v11/modules/core/keeper"

	"github.com/crossref/crossrefd/x/crossref/types"
)

type CheckpointProofVerifier func(ctx sdk.Context, clientID string, height clienttypes.Height, proof []byte, path ibcexported.Path, value []byte) error

type Keeper struct {
	storeService corestore.KVStoreService
	cdc          codec.Codec
	addressCodec address.Codec
	// Address capable of executing a MsgUpdateParams message.
	// Typically, this should be the x/gov module account.
	authority []byte

	Schema collections.Schema
	Params collections.Item[types.Params]

	Port                 collections.Item[string]
	Domains              collections.Map[string, types.DomainInfo]
	DomainChannels       collections.Map[string, types.DomainChannel]
	ChannelDomains       collections.Map[string, types.DomainChannel]
	Checkpoints          collections.Map[string, types.Checkpoint]
	LatestCheckpoints    collections.Map[string, uint64]
	CrossReferences      collections.Map[string, types.CrossReference]
	CrossReferenceAcks   collections.Map[string, types.CrossReferenceAck]
	OutgoingPackets      collections.Map[string, []byte]
	AccountabilityEvents collections.Map[string, types.AccountabilityEvent]
	TrustedLightClients  collections.Map[string, types.TrustedLightClientState]
	LightClientUpdates   collections.Map[string, types.LightClientUpdate]
	HistoryState         collections.Item[types.HistoryState]
	HistoryRounds        collections.Map[uint64, types.HistoryRound]
	historySender        func(sdk.Context, string, string, uint64, []byte) (uint64, error)

	ibcKeeperFn   func() *ibckeeper.Keeper
	proofVerifier CheckpointProofVerifier

	bankKeeper types.BankKeeper
}

func NewKeeper(
	storeService corestore.KVStoreService,
	cdc codec.Codec,
	addressCodec address.Codec,
	authority []byte,
	ibcKeeperFn func() *ibckeeper.Keeper,

	bankKeeper types.BankKeeper,
) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		storeService: storeService,
		cdc:          cdc,
		addressCodec: addressCodec,
		authority:    authority,

		bankKeeper:           bankKeeper,
		ibcKeeperFn:          ibcKeeperFn,
		Port:                 collections.NewItem(sb, types.PortKey, "port", collections.StringValue),
		Params:               collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		Domains:              collections.NewMap(sb, types.DomainKeyPrefix, "domains", collections.StringKey, codec.CollValue[types.DomainInfo](cdc)),
		DomainChannels:       collections.NewMap(sb, types.DomainChannelKeyPrefix, "domain_channels", collections.StringKey, codec.CollValue[types.DomainChannel](cdc)),
		ChannelDomains:       collections.NewMap(sb, types.ChannelDomainKeyPrefix, "channel_domains", collections.StringKey, codec.CollValue[types.DomainChannel](cdc)),
		Checkpoints:          collections.NewMap(sb, types.CheckpointKeyPrefix, "checkpoints", collections.StringKey, codec.CollValue[types.Checkpoint](cdc)),
		LatestCheckpoints:    collections.NewMap(sb, types.LatestCheckpointKeyPrefix, "latest_checkpoints", collections.StringKey, collections.Uint64Value),
		CrossReferences:      collections.NewMap(sb, types.CrossReferenceKeyPrefix, "cross_references", collections.StringKey, codec.CollValue[types.CrossReference](cdc)),
		CrossReferenceAcks:   collections.NewMap(sb, types.CrossReferenceAckKeyPrefix, "cross_reference_acks", collections.StringKey, codec.CollValue[types.CrossReferenceAck](cdc)),
		OutgoingPackets:      collections.NewMap(sb, types.OutgoingPacketKeyPrefix, "outgoing_packets", collections.StringKey, collections.BytesValue),
		AccountabilityEvents: collections.NewMap(sb, types.AccountabilityEventKeyPrefix, "accountability_events", collections.StringKey, codec.CollValue[types.AccountabilityEvent](cdc)),
		TrustedLightClients:  collections.NewMap(sb, types.TrustedLightClientKeyPrefix, "trusted_light_clients", collections.StringKey, codec.CollValue[types.TrustedLightClientState](cdc)),
		LightClientUpdates:   collections.NewMap(sb, types.LightClientUpdateKeyPrefix, "light_client_updates", collections.StringKey, codec.CollValue[types.LightClientUpdate](cdc)),
		HistoryState:         collections.NewItem(sb, collections.NewPrefix("history-v2-state/"), "history_v2_state", codec.CollValue[types.HistoryState](cdc)),
		HistoryRounds:        collections.NewMap(sb, collections.NewPrefix("history-v2-round/"), "history_v2_rounds", collections.Uint64Key, codec.CollValue[types.HistoryRound](cdc)),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

func (k *Keeper) SetCheckpointProofVerifier(verifier CheckpointProofVerifier) {
	k.proofVerifier = verifier
}

// GetAuthority returns the module's authority.
func (k Keeper) GetAuthority() []byte {
	return k.authority
}
