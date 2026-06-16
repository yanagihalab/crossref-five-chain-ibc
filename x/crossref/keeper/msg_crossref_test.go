package keeper_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	errorsmod "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
)

func TestSubmitCheckpointVerifiesHysteresisSignature(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	creator, err := f.addressCodec.BytesToString(sdk.AccAddress(bytes.Repeat([]byte{1}, 20)))
	if err != nil {
		t.Fatalf("failed to encode creator: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{
		DomainId:            "chain-a",
		ChainId:             "crossref-a",
		HysteresisPublicKey: publicKey,
	}); err != nil {
		t.Fatalf("SetDomain failed: %v", err)
	}

	checkpoint := types.Checkpoint{
		DomainId:      "chain-a",
		Height:        1,
		BlockHash:     []byte("block-a-1"),
		AppHash:       []byte("app-a-1"),
		BlockTimeUnix: 1700000000,
	}
	checkpoint.CheckpointHash = types.ComputeCheckpointHash(checkpoint)
	checkpoint.HysteresisSignature = ed25519.Sign(privateKey, types.HysteresisSignBytes(checkpoint))

	res, err := ms.SubmitCheckpoint(f.ctx, &types.MsgSubmitCheckpoint{
		Creator:             creator,
		DomainId:            checkpoint.DomainId,
		Height:              checkpoint.Height,
		BlockHash:           checkpoint.BlockHash,
		AppHash:             checkpoint.AppHash,
		CheckpointHash:      checkpoint.CheckpointHash,
		HysteresisSignature: checkpoint.HysteresisSignature,
		BlockTimeUnix:       checkpoint.BlockTimeUnix,
	})
	if err != nil {
		t.Fatalf("SubmitCheckpoint returned error: %v", err)
	}
	if !bytes.Equal(res.CheckpointHash, checkpoint.CheckpointHash) {
		t.Fatalf("checkpoint hash mismatch: got %x want %x", res.CheckpointHash, checkpoint.CheckpointHash)
	}
}

func TestSubmitCheckpointRejectsInvalidHysteresisSignature(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	creator, err := f.addressCodec.BytesToString(sdk.AccAddress(bytes.Repeat([]byte{1}, 20)))
	if err != nil {
		t.Fatalf("failed to encode creator: %v", err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{
		DomainId:            "chain-a",
		ChainId:             "crossref-a",
		HysteresisPublicKey: publicKey,
	}); err != nil {
		t.Fatalf("SetDomain failed: %v", err)
	}

	_, err = ms.SubmitCheckpoint(f.ctx, &types.MsgSubmitCheckpoint{
		Creator:             creator,
		DomainId:            "chain-a",
		Height:              1,
		BlockHash:           []byte("block-a-1"),
		AppHash:             []byte("app-a-1"),
		HysteresisSignature: []byte("not-a-valid-signature"),
		BlockTimeUnix:       1700000000,
	})
	if !errorsmod.IsOf(err, types.ErrHysteresisSignatureInvalid) {
		t.Fatalf("expected ErrHysteresisSignatureInvalid, got %v", err)
	}
}

func TestSubmitCheckpointRequiresHysteresisSignatureWhenDomainHasKey(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	creator, err := f.addressCodec.BytesToString(sdk.AccAddress(bytes.Repeat([]byte{1}, 20)))
	if err != nil {
		t.Fatalf("failed to encode creator: %v", err)
	}
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{
		DomainId:            "chain-a",
		ChainId:             "crossref-a",
		HysteresisPublicKey: publicKey,
	}); err != nil {
		t.Fatalf("SetDomain failed: %v", err)
	}

	_, err = ms.SubmitCheckpoint(f.ctx, &types.MsgSubmitCheckpoint{
		Creator:       creator,
		DomainId:      "chain-a",
		Height:        1,
		BlockHash:     []byte("block-a-1"),
		AppHash:       []byte("app-a-1"),
		BlockTimeUnix: 1700000000,
	})
	if !errorsmod.IsOf(err, types.ErrHysteresisSignatureRequired) {
		t.Fatalf("expected ErrHysteresisSignatureRequired, got %v", err)
	}
}

func TestSendCrossReferencePacketRejectsUnboundSourceDomain(t *testing.T) {
	f := initFixture(t)
	ms := keeper.NewMsgServerImpl(f.keeper)
	sender, err := f.addressCodec.BytesToString(sdk.AccAddress(bytes.Repeat([]byte{1}, 20)))
	if err != nil {
		t.Fatalf("failed to encode sender: %v", err)
	}

	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-a", ChainId: "crossref-a"}); err != nil {
		t.Fatalf("SetDomain chain-a failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-b", ChainId: "crossref-b"}); err != nil {
		t.Fatalf("SetDomain chain-b failed: %v", err)
	}
	checkpoint := types.Checkpoint{
		DomainId:      "chain-a",
		Height:        1,
		BlockHash:     []byte("block-a-1"),
		AppHash:       []byte("app-a-1"),
		BlockTimeUnix: 1700000000,
	}
	checkpoint.CheckpointHash = types.ComputeCheckpointHash(checkpoint)
	if err := f.keeper.SetCheckpoint(f.ctx, checkpoint); err != nil {
		t.Fatalf("SetCheckpoint failed: %v", err)
	}
	if err := f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{
		LocalDomainId:  "chain-b",
		RemoteDomainId: "chain-c",
		PortId:         types.PortID,
		ChannelId:      "channel-0",
	}); err != nil {
		t.Fatalf("SetDomainChannel failed: %v", err)
	}

	_, err = ms.SendCrossReferencePacket(f.ctx, &types.MsgSendCrossReferencePacket{
		Sender:         sender,
		SourceDomainId: "chain-a",
		SourceHeight:   1,
		PortId:         types.PortID,
		ChannelId:      "channel-0",
	})
	if !errorsmod.IsOf(err, types.ErrUnauthorizedChannel) {
		t.Fatalf("expected ErrUnauthorizedChannel, got %v", err)
	}
}
