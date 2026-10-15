package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"

	rpctypes "github.com/cometbft/cometbft/rpc/core/types"
	tmtypes "github.com/cometbft/cometbft/types"
)

type rpcResponse[T any] struct {
	Result T `json:"result"`
}

type envelope struct {
	Version      string                `json:"version"`
	ChainID      string                `json:"chain_id"`
	SignedHeader tmtypes.SignedHeader  `json:"signed_header"`
	ValidatorSet *tmtypes.ValidatorSet `json:"validator_set"`
}

func main() {
	if len(os.Args) != 3 {
		fatalf("usage: go run ./docker/scripts/comet-consensus-proof <rpc-url> <height>")
	}
	rpcURL := os.Args[1]
	height, err := strconv.ParseInt(os.Args[2], 10, 64)
	if err != nil || height <= 0 {
		fatalf("invalid height: %s", os.Args[2])
	}

	commit := getRPC[rpctypes.ResultCommit](fmt.Sprintf("%s/commit?height=%d", rpcURL, height))
	validators := getRPC[rpctypes.ResultValidators](fmt.Sprintf("%s/validators?height=%d&per_page=1000", rpcURL, height))
	if len(validators.Validators) != validators.Total {
		fatalf("validator pagination incomplete: got %d total %d", len(validators.Validators), validators.Total)
	}
	validatorSet := tmtypes.NewValidatorSet(validators.Validators)
	proof := envelope{
		Version:      "cometbft-signed-header-v1",
		ChainID:      commit.SignedHeader.Header.ChainID,
		SignedHeader: commit.SignedHeader,
		ValidatorSet: validatorSet,
	}
	bz, err := json.Marshal(proof)
	if err != nil {
		fatalf("marshal proof: %v", err)
	}
	fmt.Print(base64.StdEncoding.EncodeToString(bz))
}

func getRPC[T any](url string) T {
	resp, err := http.Get(url) //nolint:gosec // local experiment helper.
	if err != nil {
		fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		fatalf("GET %s returned %s: %s", url, resp.Status, string(body))
	}
	var wrapped rpcResponse[T]
	if err := json.NewDecoder(resp.Body).Decode(&wrapped); err != nil {
		fatalf("decode %s: %v", url, err)
	}
	return wrapped.Result
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
