package main

import (
	"testing"

	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	secp "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	tx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/gogoproto/proto"
	channel "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
)

func envelope(t *testing.T, wrongSigner bool) []byte {
	t.Helper()
	key := secp.GenPrivKey().PubKey().(*secp.PubKey)
	address, err := bech32.ConvertAndEncode("crossref", key.Address())
	if err != nil {
		t.Fatal(err)
	}
	if wrongSigner {
		address = "crossref1wrong"
	}
	msg := &channel.MsgRecvPacket{Signer: address, Packet: channel.Packet{Sequence: 1, SourcePort: "transfer", SourceChannel: "channel-0", DestinationPort: "transfer", DestinationChannel: "channel-1"}}
	any, err := codectypes.NewAnyWithValue(msg)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := codectypes.NewAnyWithValue(key)
	if err != nil {
		t.Fatal(err)
	}
	body, err := proto.Marshal(&tx.TxBody{Messages: []*codectypes.Any{any}})
	if err != nil {
		t.Fatal(err)
	}
	info, err := proto.Marshal(&tx.AuthInfo{SignerInfos: []*tx.SignerInfo{{PublicKey: pk, Sequence: 7}}, Fee: &tx.Fee{GasLimit: 100000}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := proto.Marshal(&tx.TxRaw{BodyBytes: body, AuthInfoBytes: info, Signatures: [][]byte{make([]byte, 64)}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodedEnvelope(t *testing.T) {
	result, err := decode(envelope(t, false))
	if err != nil {
		t.Fatal(err)
	}
	v := result.(map[string]any)
	if v["sequence"] != "7" || v["messageCount"] != 1 || len(v["packets"].([]packet)) != 1 {
		t.Fatal("wrong public metadata")
	}
	for _, key := range []string{"signature", "raw", "bodyBytes", "pubkey"} {
		if _, ok := v[key]; ok {
			t.Fatal("private envelope exposed")
		}
	}
}

func TestRejectWrongSignerAndMalformed(t *testing.T) {
	for _, raw := range [][]byte{envelope(t, true), {0xff, 0xff}, {}} {
		if _, err := decode(raw); err == nil {
			t.Fatal("accepted invalid envelope")
		}
	}
}
