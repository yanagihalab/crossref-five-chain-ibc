// Narrow typed governance queries for the live experiment CLI.
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	db "github.com/cometbft/cometbft-db"
	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	state "github.com/cometbft/cometbft/state"
	address "github.com/cosmos/cosmos-sdk/codec/address"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gov "github.com/cosmos/cosmos-sdk/x/gov/types/v1"
	crossref "github.com/crossref/crossrefd/x/crossref/types"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

func decodePacket(encoded string, input io.Reader) (map[string]any, error) {
	var bz []byte
	var err error
	if input != nil {
		if encoded != "" {
			return nil, fmt.Errorf("choose packet hex or stdin, not both")
		}
		bz, err = io.ReadAll(io.LimitReader(input, crossref.MaxHistoryPacketBytes+1))
	} else {
		if len(encoded) > crossref.MaxHistoryPacketBytes*2 {
			return nil, fmt.Errorf("oversized history packet")
		}
		bz, err = hex.DecodeString(encoded)
	}
	if err != nil || len(bz) == 0 || len(bz) > crossref.MaxHistoryPacketBytes {
		return nil, fmt.Errorf("invalid/oversized history packet")
	}
	var wire crossref.CrossrefPacketData
	if err := wire.Unmarshal(bz); err != nil {
		return nil, err
	}
	h := wire.GetHistory()
	if h == nil {
		return nil, fmt.Errorf("not a history packet")
	}
	kind := "part"
	if h.Seal != nil {
		kind = "seal"
	}
	return map[string]any{"round": h.Round, "source": h.SourceDomainId, "kind": kind}, nil
}

func run() error {
	node := flag.String("node", "", "trusted RPC URL")
	proposal := flag.Uint64("proposal", 0, "proposal ID; zero prints governance authority")
	packet := flag.String("packet-hex", "", "decode public history packet metadata")
	packetStdin := flag.Bool("packet-stdin", false, "decode bounded binary history packet from standard input")
	stateDir := flag.String("state-dir", "", "stopped node data directory, read-only")
	through := flag.Int64("through", 0, "read retained block results 1..height")
	flag.Parse()
	if *stateDir != "" {
		if *through < 1 || *through > 10000 {
			return fmt.Errorf("invalid retained block range")
		}
		database, err := db.NewGoLevelDBWithOpts("state", *stateDir, &opt.Options{ReadOnly: true, ErrorIfMissing: true})
		if err != nil {
			return err
		}
		defer database.Close()
		store := state.NewStore(database, state.StoreOptions{})
		for h := int64(1); h <= *through; h++ {
			r, err := store.LoadFinalizeBlockResponse(h)
			if err != nil {
				return err
			}
			if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"height": fmt.Sprint(h), "finalize_block_events": r.Events, "txs_results": r.TxResults}); err != nil {
				return err
			}
		}
		return nil
	}
	if *packet != "" || *packetStdin {
		var input io.Reader
		if *packetStdin {
			input = os.Stdin
		}
		metadata, err := decodePacket(*packet, input)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(metadata)
	}
	if *proposal == 0 {
		a, err := address.NewBech32Codec("crossref").BytesToString(authtypes.NewModuleAddress("gov"))
		if err != nil {
			return err
		}
		fmt.Println(a)
		return nil
	}
	rpc, err := rpchttp.New(*node, "/websocket")
	if err != nil {
		return err
	}
	request := &gov.QueryProposalRequest{ProposalId: *proposal}
	bz, err := request.Marshal()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := rpc.ABCIQuery(ctx, "/cosmos.gov.v1.Query/Proposal", bz)
	if err != nil {
		return err
	}
	if result.Response.Code != 0 {
		return fmt.Errorf("governance query code %d", result.Response.Code)
	}
	var response gov.QueryProposalResponse
	if err := response.Unmarshal(result.Response.Value); err != nil {
		return err
	}
	if response.Proposal == nil {
		return fmt.Errorf("proposal missing")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"id": response.Proposal.Id, "status": response.Proposal.Status.String(), "voting_end_time": response.Proposal.VotingEndTime})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
