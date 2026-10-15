package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	secp "github.com/cosmos/cosmos-sdk/crypto/keys/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	tx "github.com/cosmos/cosmos-sdk/types/tx"
	auth "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/gogoproto/proto"
	client "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	channel "github.com/cosmos/ibc-go/v11/modules/core/04-channel/types"
)

type packet struct {
	SourcePort         string `json:"sourcePort"`
	SourceChannel      string `json:"sourceChannel"`
	DestinationPort    string `json:"destinationPort"`
	DestinationChannel string `json:"destinationChannel"`
	Sequence           string `json:"sequence"`
	Action             string `json:"action"`
	Outcome            string `json:"outcome"`
	Signer             string `json:"signer"`
}

func publicPacket(p channel.Packet, action, outcome, signer string) packet {
	return packet{p.SourcePort, p.SourceChannel, p.DestinationPort, p.DestinationChannel, strconv.FormatUint(p.Sequence, 10), action, outcome, signer}
}

func decode(raw []byte) (any, error) {
	var outer tx.TxRaw
	if err := proto.Unmarshal(raw, &outer); err != nil {
		return nil, err
	}
	if len(outer.Signatures) != 1 || len(outer.Signatures[0]) != 64 {
		return nil, fmt.Errorf("one secp256k1 signature required")
	}
	var body tx.TxBody
	var info tx.AuthInfo
	if err := proto.Unmarshal(outer.BodyBytes, &body); err != nil {
		return nil, err
	}
	if err := proto.Unmarshal(outer.AuthInfoBytes, &info); err != nil {
		return nil, err
	}
	if len(body.Messages) == 0 || len(body.Messages) > 30 || len(body.ExtensionOptions) != 0 || len(body.NonCriticalExtensionOptions) != 0 {
		return nil, fmt.Errorf("unsupported body")
	}
	if len(info.SignerInfos) != 1 || info.SignerInfos[0].PublicKey == nil || info.SignerInfos[0].PublicKey.TypeUrl != "/cosmos.crypto.secp256k1.PubKey" {
		return nil, fmt.Errorf("unsupported signer")
	}
	if info.Fee == nil || info.Fee.Granter != "" || info.Fee.Payer != "" {
		return nil, fmt.Errorf("unsupported fee payer")
	}
	var key secp.PubKey
	if err := proto.Unmarshal(info.SignerInfos[0].PublicKey.Value, &key); err != nil {
		return nil, err
	}
	if len(key.Key) != 33 {
		return nil, fmt.Errorf("invalid key")
	}
	address, err := bech32.ConvertAndEncode("crossref", key.Address())
	if err != nil {
		return nil, err
	}
	packets := []packet{}
	updates := 0
	for _, m := range body.Messages {
		switch m.TypeUrl {
		case "/ibc.core.channel.v1.MsgRecvPacket":
			var v channel.MsgRecvPacket
			if err := proto.Unmarshal(m.Value, &v); err != nil {
				return nil, err
			}
			packets = append(packets, publicPacket(v.Packet, "receive", "received", v.Signer))
		case "/ibc.core.channel.v1.MsgAcknowledgement":
			var v channel.MsgAcknowledgement
			if err := proto.Unmarshal(m.Value, &v); err != nil {
				return nil, err
			}
			packets = append(packets, publicPacket(v.Packet, "ack", "acknowledged", v.Signer))
		case "/ibc.core.channel.v1.MsgTimeout":
			var v channel.MsgTimeout
			if err := proto.Unmarshal(m.Value, &v); err != nil {
				return nil, err
			}
			packets = append(packets, publicPacket(v.Packet, "receive", "timed-out", v.Signer))
		case "/ibc.core.channel.v1.MsgTimeoutOnClose":
			var v channel.MsgTimeoutOnClose
			if err := proto.Unmarshal(m.Value, &v); err != nil {
				return nil, err
			}
			packets = append(packets, publicPacket(v.Packet, "receive", "timed-out", v.Signer))
		case "/ibc.core.client.v1.MsgUpdateClient":
			var v client.MsgUpdateClient
			if err := proto.Unmarshal(m.Value, &v); err != nil {
				return nil, err
			}
			if v.Signer != address {
				return nil, fmt.Errorf("wrong signer")
			}
			updates++
		default:
			return nil, fmt.Errorf("message outside pool scope")
		}
	}
	for _, p := range packets {
		if p.Signer != address || p.Sequence == "0" {
			return nil, fmt.Errorf("wrong packet signer")
		}
	}
	hash := sha256.Sum256(raw)
	return map[string]any{"hash": strings.ToUpper(hex.EncodeToString(hash[:])), "address": address, "sequence": strconv.FormatUint(info.SignerInfos[0].Sequence, 10), "packets": packets, "clientUpdates": updates, "messageCount": len(body.Messages), "gasLimit": strconv.FormatUint(info.Fee.GasLimit, 10), "fee": info.Fee.Amount.String()}, nil
}

func main() {
	// Input is bounded and never logged: it may contain signed transaction bytes.
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		os.Exit(1)
	}
	var input struct {
		Tx           string `json:"tx"`
		Address      string `json:"address"`
		AccountValue string `json:"accountValue"`
	}
	if json.Unmarshal(data, &input) != nil {
		os.Exit(1)
	}
	var result any
	if input.Address != "" {
		if input.AccountValue == "" {
			v := auth.QueryAccountRequest{Address: input.Address}
			var raw []byte
			raw, err = proto.Marshal(&v)
			result = map[string]string{"data": hex.EncodeToString(raw)}
		} else {
			var raw []byte
			raw, err = base64.StdEncoding.DecodeString(input.AccountValue)
			var response auth.QueryAccountResponse
			if err == nil {
				err = proto.Unmarshal(raw, &response)
			}
			if err == nil && (response.Account == nil || response.Account.TypeUrl != "/cosmos.auth.v1beta1.BaseAccount") {
				err = fmt.Errorf("unsupported account")
			}
			var account auth.BaseAccount
			if err == nil {
				err = proto.Unmarshal(response.Account.Value, &account)
			}
			if err == nil && account.Address != input.Address {
				err = fmt.Errorf("wrong account")
			}
			result = map[string]string{"sequence": strconv.FormatUint(account.Sequence, 10)}
		}
	} else {
		var raw []byte
		raw, err = base64.StdEncoding.DecodeString(input.Tx)
		if err == nil {
			result, err = decode(raw)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pool decoder rejected input")
		os.Exit(1)
	}
	if json.NewEncoder(os.Stdout).Encode(result) != nil {
		os.Exit(1)
	}
}
