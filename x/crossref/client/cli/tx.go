package cli

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

var DefaultRelativePacketTimeoutTimestamp = uint64((time.Duration(10) * time.Minute).Nanoseconds())

const listSeparator = ","

// GetTxCmd returns the transaction commands for this module.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      fmt.Sprintf("%s transactions subcommands", types.ModuleName),
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(
		CmdRegisterDomain(),
		CmdBindDomainChannel(),
		CmdSubmitCheckpoint(),
		CmdSendCrossReferencePacket(),
		CmdBroadcastCrossReferencePacket(),
	)

	return cmd
}

func CmdRegisterDomain() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register-domain [creator] [domain-id] [chain-id]",
		Short: "Registers a crossref domain",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			creator, err := signerAddress(clientCtx, args[0])
			if err != nil {
				return err
			}
			validatorSetHash, err := bytesFlag(cmd, "validator-set-hash")
			if err != nil {
				return err
			}
			hysteresisPublicKey, err := bytesFlag(cmd, "hysteresis-public-key")
			if err != nil {
				return err
			}
			metadataURI, err := cmd.Flags().GetString("metadata-uri")
			if err != nil {
				return err
			}
			keyEpoch, err := cmd.Flags().GetUint64("key-epoch")
			if err != nil {
				return err
			}
			admin, err := cmd.Flags().GetString("admin")
			if err != nil {
				return err
			}

			msg := &types.MsgRegisterDomain{
				Creator:             creator,
				DomainId:            args[1],
				ChainId:             args[2],
				ValidatorSetHash:    validatorSetHash,
				MetadataUri:         metadataURI,
				HysteresisPublicKey: hysteresisPublicKey,
				KeyEpoch:            keyEpoch,
				Admin:               admin,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("validator-set-hash", "", "Base64-encoded validator set hash")
	cmd.Flags().String("metadata-uri", "", "Domain metadata URI")
	cmd.Flags().String("hysteresis-public-key", "", "Base64-encoded Ed25519 or threshold hysteresis public key")
	cmd.Flags().Uint64("key-epoch", 0, "Hysteresis signing key epoch")
	cmd.Flags().String("admin", "", "Domain admin address; defaults to creator")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdBindDomainChannel() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "bind-domain-channel [creator] [local-domain-id] [remote-domain-id] [port-id] [channel-id]",
		Short: "Binds a remote domain to an IBC channel",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			creator, err := signerAddress(clientCtx, args[0])
			if err != nil {
				return err
			}
			clientID, err := cmd.Flags().GetString("client-id")
			if err != nil {
				return err
			}
			counterpartyPortID, err := cmd.Flags().GetString("counterparty-port-id")
			if err != nil {
				return err
			}
			counterpartyChannelID, err := cmd.Flags().GetString("counterparty-channel-id")
			if err != nil {
				return err
			}

			msg := &types.MsgBindDomainChannel{
				Creator:               creator,
				LocalDomainId:         args[1],
				RemoteDomainId:        args[2],
				PortId:                args[3],
				ChannelId:             args[4],
				ClientId:              clientID,
				CounterpartyPortId:    counterpartyPortID,
				CounterpartyChannelId: counterpartyChannelID,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("client-id", "", "IBC client ID for the remote domain")
	cmd.Flags().String("counterparty-port-id", "", "Counterparty port ID")
	cmd.Flags().String("counterparty-channel-id", "", "Counterparty channel ID")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdSubmitCheckpoint() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "submit-checkpoint [creator] [domain-id] [checkpoint-height] [block-hash] [app-hash]",
		Short: "Submits a local checkpoint",
		Args:  cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			creator, err := signerAddress(clientCtx, args[0])
			if err != nil {
				return err
			}
			height, err := strconv.ParseUint(args[2], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid checkpoint height: %w", err)
			}
			blockHash, err := decodeBase64Arg("block-hash", args[3])
			if err != nil {
				return err
			}
			appHash, err := decodeBase64Arg("app-hash", args[4])
			if err != nil {
				return err
			}
			validatorSetHash, err := bytesFlag(cmd, "validator-set-hash")
			if err != nil {
				return err
			}
			previousCheckpointHash, err := bytesFlag(cmd, "previous-checkpoint-hash")
			if err != nil {
				return err
			}
			checkpointHash, err := bytesFlag(cmd, "checkpoint-hash")
			if err != nil {
				return err
			}
			hysteresisSignature, err := bytesFlag(cmd, "hysteresis-signature")
			if err != nil {
				return err
			}
			blockTimeUnix, err := cmd.Flags().GetInt64("block-time-unix")
			if err != nil {
				return err
			}
			stateHash, err := bytesFlag(cmd, "state-hash")
			if err != nil {
				return err
			}
			previousStateHash, err := bytesFlag(cmd, "previous-state-hash")
			if err != nil {
				return err
			}
			consensusProof, consensusProofRevisionNumber, consensusProofRevisionHeight, err := consensusProofFlags(cmd)
			if err != nil {
				return err
			}
			keyEpoch, err := cmd.Flags().GetUint64("key-epoch")
			if err != nil {
				return err
			}

			msg := &types.MsgSubmitCheckpoint{
				Creator:                      creator,
				DomainId:                     args[1],
				Height:                       height,
				BlockHash:                    blockHash,
				AppHash:                      appHash,
				ValidatorSetHash:             validatorSetHash,
				PreviousCheckpointHash:       previousCheckpointHash,
				CheckpointHash:               checkpointHash,
				HysteresisSignature:          hysteresisSignature,
				BlockTimeUnix:                blockTimeUnix,
				StateHash:                    stateHash,
				PreviousStateHash:            previousStateHash,
				ConsensusProof:               consensusProof,
				ConsensusProofRevisionNumber: consensusProofRevisionNumber,
				ConsensusProofRevisionHeight: consensusProofRevisionHeight,
				KeyEpoch:                     keyEpoch,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("validator-set-hash", "", "Base64-encoded validator set hash")
	cmd.Flags().String("previous-checkpoint-hash", "", "Base64-encoded previous checkpoint hash")
	cmd.Flags().String("checkpoint-hash", "", "Base64-encoded checkpoint hash")
	cmd.Flags().String("hysteresis-signature", "", "Base64-encoded hysteresis signature")
	cmd.Flags().Int64("block-time-unix", 0, "Checkpoint block time as Unix seconds")
	cmd.Flags().String("state-hash", "", "Base64-encoded paper state hash H(S_n)")
	cmd.Flags().String("previous-state-hash", "", "Base64-encoded previous paper state hash H(S_n-1)")
	cmd.Flags().String("consensus-proof", "", "Base64-encoded consensus proof envelope")
	cmd.Flags().Uint64("consensus-proof-revision-number", 0, "Consensus proof revision number")
	cmd.Flags().Uint64("consensus-proof-revision-height", 0, "Consensus proof revision height")
	cmd.Flags().Uint64("key-epoch", 0, "Hysteresis signing key epoch")
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdSendCrossReferencePacket() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send-cross-reference-packet [sender] [source-domain-id] [source-height] [port-id] [channel-id]",
		Short: "Sends a checkpoint as an IBC packet",
		Args:  cobra.RangeArgs(3, 5),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			sender, err := signerAddress(clientCtx, args[0])
			if err != nil {
				return err
			}
			sourceHeight, err := strconv.ParseUint(args[2], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid source height: %w", err)
			}
			timeoutSeconds, proof, revisionNumber, revisionHeight, err := packetProofFlags(cmd)
			if err != nil {
				return err
			}
			consensusProof, consensusProofRevisionNumber, consensusProofRevisionHeight, err := consensusProofFlags(cmd)
			if err != nil {
				return err
			}
			allBoundChannels, err := cmd.Flags().GetBool("all-bound-channels")
			if err != nil {
				return err
			}
			if allBoundChannels {
				portID := types.PortID
				if len(args) >= 4 {
					portID = args[3]
				}
				excludeRemoteDomainIDs, err := cmd.Flags().GetStringSlice("exclude-remote-domain-ids")
				if err != nil {
					return err
				}
				msg := &types.MsgBroadcastCrossReferencePacket{
					Sender:                       sender,
					SourceDomainId:               args[1],
					SourceHeight:                 sourceHeight,
					PortId:                       portID,
					ExcludeRemoteDomainIds:       excludeRemoteDomainIDs,
					TimeoutSeconds:               timeoutSeconds,
					SourceCheckpointProof:        proof,
					SourceProofRevisionNumber:    revisionNumber,
					SourceProofRevisionHeight:    revisionHeight,
					ConsensusProof:               consensusProof,
					ConsensusProofRevisionNumber: consensusProofRevisionNumber,
					ConsensusProofRevisionHeight: consensusProofRevisionHeight,
				}
				return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
			}
			if len(args) != 5 {
				return fmt.Errorf("port-id and channel-id are required unless --all-bound-channels is set")
			}

			msg := &types.MsgSendCrossReferencePacket{
				Sender:                       sender,
				SourceDomainId:               args[1],
				SourceHeight:                 sourceHeight,
				PortId:                       args[3],
				ChannelId:                    args[4],
				TimeoutSeconds:               timeoutSeconds,
				SourceCheckpointProof:        proof,
				SourceProofRevisionNumber:    revisionNumber,
				SourceProofRevisionHeight:    revisionHeight,
				ConsensusProof:               consensusProof,
				ConsensusProofRevisionNumber: consensusProofRevisionNumber,
				ConsensusProofRevisionHeight: consensusProofRevisionHeight,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().Bool("all-bound-channels", false, "Send one broadcast tx that emits packets on every bound channel for the source domain")
	cmd.Flags().StringSlice("exclude-remote-domain-ids", nil, "Remote domain IDs to exclude when --all-bound-channels is set")
	addPacketProofFlags(cmd)
	addConsensusProofFlags(cmd)
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdBroadcastCrossReferencePacket() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "broadcast-cross-reference-packet [sender] [source-domain-id] [source-height]",
		Short: "Broadcasts a checkpoint to every bound remote domain",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			sender, err := signerAddress(clientCtx, args[0])
			if err != nil {
				return err
			}
			sourceHeight, err := strconv.ParseUint(args[2], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid source height: %w", err)
			}
			portID, err := cmd.Flags().GetString("port-id")
			if err != nil {
				return err
			}
			excludeRemoteDomainIDs, err := cmd.Flags().GetStringSlice("exclude-remote-domain-ids")
			if err != nil {
				return err
			}
			timeoutSeconds, proof, revisionNumber, revisionHeight, err := packetProofFlags(cmd)
			if err != nil {
				return err
			}
			consensusProof, consensusProofRevisionNumber, consensusProofRevisionHeight, err := consensusProofFlags(cmd)
			if err != nil {
				return err
			}

			msg := &types.MsgBroadcastCrossReferencePacket{
				Sender:                       sender,
				SourceDomainId:               args[1],
				SourceHeight:                 sourceHeight,
				PortId:                       portID,
				ExcludeRemoteDomainIds:       excludeRemoteDomainIDs,
				TimeoutSeconds:               timeoutSeconds,
				SourceCheckpointProof:        proof,
				SourceProofRevisionNumber:    revisionNumber,
				SourceProofRevisionHeight:    revisionHeight,
				ConsensusProof:               consensusProof,
				ConsensusProofRevisionNumber: consensusProofRevisionNumber,
				ConsensusProofRevisionHeight: consensusProofRevisionHeight,
			}
			return clienttx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	cmd.Flags().String("port-id", types.PortID, "IBC port ID to broadcast from")
	cmd.Flags().StringSlice("exclude-remote-domain-ids", nil, "Remote domain IDs to exclude")
	addPacketProofFlags(cmd)
	addConsensusProofFlags(cmd)
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func addPacketProofFlags(cmd *cobra.Command) {
	cmd.Flags().Uint64("timeout-seconds", 0, "IBC packet timeout in seconds")
	cmd.Flags().String("source-checkpoint-proof", "", "Base64-encoded source checkpoint ICS23 proof")
	cmd.Flags().Uint64("source-proof-revision-number", 0, "Source proof revision number")
	cmd.Flags().Uint64("source-proof-revision-height", 0, "Source proof revision height")
}

func addConsensusProofFlags(cmd *cobra.Command) {
	cmd.Flags().String("consensus-proof", "", "Base64-encoded consensus proof envelope")
	cmd.Flags().Uint64("consensus-proof-revision-number", 0, "Consensus proof revision number")
	cmd.Flags().Uint64("consensus-proof-revision-height", 0, "Consensus proof revision height")
}

func consensusProofFlags(cmd *cobra.Command) ([]byte, uint64, uint64, error) {
	proof, err := bytesFlag(cmd, "consensus-proof")
	if err != nil {
		return nil, 0, 0, err
	}
	revisionNumber, err := cmd.Flags().GetUint64("consensus-proof-revision-number")
	if err != nil {
		return nil, 0, 0, err
	}
	revisionHeight, err := cmd.Flags().GetUint64("consensus-proof-revision-height")
	if err != nil {
		return nil, 0, 0, err
	}
	return proof, revisionNumber, revisionHeight, nil
}

func packetProofFlags(cmd *cobra.Command) (uint64, []byte, uint64, uint64, error) {
	timeoutSeconds, err := cmd.Flags().GetUint64("timeout-seconds")
	if err != nil {
		return 0, nil, 0, 0, err
	}
	proof, err := bytesFlag(cmd, "source-checkpoint-proof")
	if err != nil {
		return 0, nil, 0, 0, err
	}
	revisionNumber, err := cmd.Flags().GetUint64("source-proof-revision-number")
	if err != nil {
		return 0, nil, 0, 0, err
	}
	revisionHeight, err := cmd.Flags().GetUint64("source-proof-revision-height")
	if err != nil {
		return 0, nil, 0, 0, err
	}
	return timeoutSeconds, proof, revisionNumber, revisionHeight, nil
}

func signerAddress(clientCtx client.Context, value string) (string, error) {
	if _, err := sdk.AccAddressFromBech32(value); err == nil {
		return value, nil
	}
	if clientCtx.GetFromAddress() == nil {
		return "", fmt.Errorf("%s is not an address and --from did not resolve an address", value)
	}
	return clientCtx.GetFromAddress().String(), nil
}

func bytesFlag(cmd *cobra.Command, name string) ([]byte, error) {
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return nil, err
	}
	return decodeBase64Arg(name, value)
}

func decodeBase64Arg(name, value string) ([]byte, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	bz, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 %s: %w", name, err)
	}
	return bz, nil
}
