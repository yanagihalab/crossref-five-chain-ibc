package crossref

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	"github.com/crossref/crossrefd/x/crossref/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Query_serviceDesc.ServiceName,
			EnhanceCustomCommand: true,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "History", Use: "history", Short: "Shows automatic history round and canonical signing bytes"},
				{RpcMethod: "HistoryRound", Use: "history-round [round]", Short: "Shows a stored history round", PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}}},
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Shows the parameters of the module",
				},
				{
					RpcMethod:      "Domain",
					Use:            "domain [domain-id]",
					Short:          "Shows a registered crossref domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "domain_id"}},
				},
				{
					RpcMethod: "Checkpoint",
					Use:       "checkpoint [domain-id] [checkpoint-height]",
					Short:     "Shows a checkpoint by domain and height",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "domain_id"},
						{ProtoField: "height"},
					},
				},
				{
					RpcMethod:      "LatestCheckpoint",
					Use:            "latest-checkpoint [domain-id]",
					Short:          "Shows the latest checkpoint for a domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "domain_id"}},
				},
				{
					RpcMethod: "CrossReference",
					Use:       "cross-reference [local-domain-id] [remote-domain-id] [remote-height]",
					Short:     "Shows an accepted cross-reference",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "local_domain_id"},
						{ProtoField: "remote_domain_id"},
						{ProtoField: "remote_height"},
					},
				},
				{
					RpcMethod: "CrossReferenceAck",
					Use:       "crossref-ack [port-id] [channel-id] [packet-sequence]",
					Short:     "Shows a Crossref application ACK stored after an IBC acknowledgement",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "port_id"},
						{ProtoField: "channel_id"},
						{ProtoField: "packet_sequence"},
					},
				},
				{
					RpcMethod:      "AccountabilityEvent",
					Use:            "accountability-event [event-id]",
					Short:          "Shows an accountability event",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "event_id"}},
				},
				{
					RpcMethod: "AccountabilityEvents",
					Use:       "accountability-events",
					Short:     "Lists accountability events",
				},
				{
					RpcMethod:      "TrustedLightClientState",
					Use:            "trusted-light-client-state [domain-id]",
					Short:          "Shows the trusted light-client state for a domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "domain_id"}},
				},
				{
					RpcMethod:      "LightClientUpdates",
					Use:            "light-client-updates [domain-id]",
					Short:          "Lists trusted light-client updates for a domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "domain_id"}},
				},
			},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service:              types.Msg_serviceDesc.ServiceName,
			EnhanceCustomCommand: true, // only required if you want to use the custom command
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{RpcMethod: "ConfigureHistory", Skip: true},
				{RpcMethod: "AttachHistoryProof", Skip: true},
				{RpcMethod: "SignHistoryRound", Use: "sign-history-round [round] [signature]", Short: "Submits an Ed25519 signature over the complete history round", PositionalArgs: []*autocliv1.PositionalArgDescriptor{{ProtoField: "round"}, {ProtoField: "signature"}}},
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // skipped because authority gated
				},
				{
					RpcMethod: "RegisterDomain",
					Use:       "register-domain [creator] [domain-id] [chain-id]",
					Short:     "Registers a crossref domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "creator"},
						{ProtoField: "domain_id"},
						{ProtoField: "chain_id"},
					},
				},
				{
					RpcMethod: "BindDomainChannel",
					Use:       "bind-domain-channel [creator] [local-domain-id] [remote-domain-id] [port-id] [channel-id]",
					Short:     "Binds a remote domain to an IBC channel",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "creator"},
						{ProtoField: "local_domain_id"},
						{ProtoField: "remote_domain_id"},
						{ProtoField: "port_id"},
						{ProtoField: "channel_id"},
					},
				},
				{
					RpcMethod: "SubmitCheckpoint",
					Use:       "submit-checkpoint [creator] [domain-id] [checkpoint-height] [block-hash] [app-hash]",
					Short:     "Submits a local checkpoint",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "creator"},
						{ProtoField: "domain_id"},
						{ProtoField: "height"},
						{ProtoField: "block_hash"},
						{ProtoField: "app_hash"},
					},
				},
				{
					RpcMethod: "SendCrossReferencePacket",
					Use:       "send-cross-reference-packet [sender] [source-domain-id] [source-height] [port-id] [channel-id]",
					Short:     "Sends a checkpoint as an IBC packet",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sender"},
						{ProtoField: "source_domain_id"},
						{ProtoField: "source_height"},
						{ProtoField: "port_id"},
						{ProtoField: "channel_id"},
					},
				},
				{
					RpcMethod: "BroadcastCrossReferencePacket",
					Use:       "broadcast-cross-reference-packet [sender] [source-domain-id] [source-height]",
					Short:     "Broadcasts a checkpoint to every bound remote domain",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "sender"},
						{ProtoField: "source_domain_id"},
						{ProtoField: "source_height"},
					},
				},
			},
		},
	}
}
