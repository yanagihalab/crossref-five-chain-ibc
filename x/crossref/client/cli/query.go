package cli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	clienttypes "github.com/cosmos/ibc-go/v10/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v10/modules/core/23-commitment/types"
	"github.com/spf13/cobra"

	"github.com/crossref/crossrefd/x/crossref/types"
)

type checkpointProofResponse struct {
	DomainID                  string `json:"domain_id"`
	Height                    uint64 `json:"height"`
	StoreKey                  string `json:"store_key"`
	SourceCheckpointProof     string `json:"source_checkpoint_proof"`
	SourceProofRevisionNumber uint64 `json:"source_proof_revision_number"`
	SourceProofRevisionHeight uint64 `json:"source_proof_revision_height"`
}

// GetQueryCmd returns the query commands for this module.
func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      fmt.Sprintf("%s query subcommands", types.ModuleName),
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(CmdCheckpointProof())

	return cmd
}

func CmdCheckpointProof() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "checkpoint-proof [domain-id] [height]",
		Short: "Queries an ICS23 proof for a source checkpoint",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}

			height, err := strconv.ParseUint(args[1], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid checkpoint height: %w", err)
			}

			key, value, proof, proofHeight, err := queryCheckpointProof(clientCtx, args[0], height)
			if err != nil {
				return err
			}
			if len(value) == 0 {
				return fmt.Errorf("checkpoint not found: domain=%s height=%d", args[0], height)
			}

			response := checkpointProofResponse{
				DomainID:                  args[0],
				Height:                    height,
				StoreKey:                  base64.StdEncoding.EncodeToString(key),
				SourceCheckpointProof:     base64.StdEncoding.EncodeToString(proof),
				SourceProofRevisionNumber: proofHeight.GetRevisionNumber(),
				SourceProofRevisionHeight: proofHeight.GetRevisionHeight(),
			}

			encoder := json.NewEncoder(cmd.OutOrStdout())
			return encoder.Encode(response)
		},
	}

	flags.AddQueryFlagsToCmd(cmd)

	return cmd
}

func queryCheckpointProof(clientCtx client.Context, domainID string, checkpointHeight uint64) ([]byte, []byte, []byte, clienttypes.Height, error) {
	key, err := types.CheckpointStorageKey(domainID, checkpointHeight)
	if err != nil {
		return nil, nil, nil, clienttypes.Height{}, err
	}

	queryHeight := clientCtx.Height
	if queryHeight != 0 && queryHeight <= 2 {
		return nil, nil, nil, clienttypes.Height{}, errors.New("proof queries at height <= 2 are not supported")
	}
	if queryHeight != 0 {
		queryHeight--
	}

	res, err := clientCtx.QueryABCI(abci.RequestQuery{
		Path:   fmt.Sprintf("store/%s/key", types.StoreKey),
		Height: queryHeight,
		Data:   key,
		Prove:  true,
	})
	if err != nil {
		return nil, nil, nil, clienttypes.Height{}, err
	}
	if res.ProofOps == nil {
		return nil, nil, nil, clienttypes.Height{}, errors.New("ABCI response did not include proof ops")
	}

	merkleProof, err := commitmenttypes.ConvertProofs(res.ProofOps)
	if err != nil {
		return nil, nil, nil, clienttypes.Height{}, err
	}

	cdc := codec.NewProtoCodec(clientCtx.InterfaceRegistry)
	proofBz, err := cdc.Marshal(&merkleProof)
	if err != nil {
		return nil, nil, nil, clienttypes.Height{}, err
	}

	revision := clienttypes.ParseChainID(clientCtx.ChainID)
	return key, res.Value, proofBz, clienttypes.NewHeight(revision, uint64(res.Height)+1), nil
}
