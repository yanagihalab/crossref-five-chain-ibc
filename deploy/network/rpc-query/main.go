// Decode the protobuf payload returned by the existing ABCI Params query.
package main

import (
	"encoding/base64"
	"fmt"
	"os"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

func decode(value string) ([]byte, error) {
	bz, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(bz) == 0 || len(bz) > 65536 {
		return nil, fmt.Errorf("invalid Params payload size")
	}
	var response types.QueryParamsResponse
	if err := response.Unmarshal(bz); err != nil {
		return nil, err
	}
	return codec.NewProtoCodec(codectypes.NewInterfaceRegistry()).MarshalJSON(&response)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: rpc-query BASE64_PARAMS")
		os.Exit(1)
	}
	value, err := decode(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(value))
}
