package crossref

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"

	"cosmossdk.io/core/address"
	errorsmod "cosmossdk.io/errors"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"

	"github.com/crossref/crossrefd/x/crossref/keeper"
	"github.com/crossref/crossrefd/x/crossref/types"
)

type ibcFixture struct {
	ctx          sdk.Context
	ibcModule    IBCModule
	keeper       keeper.Keeper
	addressCodec address.Codec
}

func initIBCFixture(t *testing.T) *ibcFixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig(AppModule{})
	addressCodec := addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix())
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx
	authority := authtypes.NewModuleAddress(govtypes.ModuleName)

	k := keeper.NewKeeper(
		storeService,
		encCfg.Codec,
		addressCodec,
		authority,
		nil,
		nil,
	)
	if err := k.Params.Set(ctx, types.DefaultParams()); err != nil {
		t.Fatalf("failed to set params: %v", err)
	}
	k.SetCheckpointProofVerifier(func(ctx sdk.Context, clientID string, height clienttypes.Height, proof []byte, path ibcexported.Path, value []byte) error {
		if clientID != "client-a" {
			return fmt.Errorf("unexpected client id: %s", clientID)
		}
		if height.GetRevisionNumber() != 1 || height.GetRevisionHeight() != 10 {
			return fmt.Errorf("unexpected proof height: %s", height)
		}
		if !bytes.Equal(proof, []byte("proof-a-1")) {
			return fmt.Errorf("unexpected proof bytes: %x", proof)
		}
		if len(value) == 0 {
			return fmt.Errorf("empty proof value")
		}
		if path.Empty() {
			return fmt.Errorf("empty proof path")
		}
		return nil
	})

	return &ibcFixture{
		ctx:          ctx,
		ibcModule:    NewIBCModule(encCfg.Codec, k),
		keeper:       k,
		addressCodec: addressCodec,
	}
}

func TestReceiveCrossReferencePacketVerifiesSourceDomainAndChain(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()

	hash, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if err != nil {
		t.Fatalf("receiveCrossReferencePacket returned error: %v", err)
	}
	if !bytes.Equal(hash, packet.CheckpointHash) {
		t.Fatalf("stored hash mismatch: got %x want %x", hash, packet.CheckpointHash)
	}

	reference, found, err := f.keeper.GetCrossReference(f.ctx, "chain-b", "chain-a", 1)
	if err != nil {
		t.Fatalf("GetCrossReference returned error: %v", err)
	}
	if !found {
		t.Fatal("cross-reference was not stored")
	}
	if !bytes.Equal(reference.RemoteCheckpointHash, packet.CheckpointHash) {
		t.Fatalf("reference hash mismatch: got %x want %x", reference.RemoteCheckpointHash, packet.CheckpointHash)
	}
}

func TestReceiveCrossReferencePacketRejectsSourceChainMismatch(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()
	packet.SourceChainId = "forged-chain"

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrSourceChainMismatch) {
		t.Fatalf("expected ErrSourceChainMismatch, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsMissingSourceDomain(t *testing.T) {
	f := initIBCFixture(t)
	packet := validCrossReferencePacket()
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-b", ChainId: "crossref-b"}); err != nil {
		t.Fatalf("SetDomain local failed: %v", err)
	}
	if err := f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{
		LocalDomainId:  "chain-b",
		RemoteDomainId: "chain-a",
		PortId:         types.PortID,
		ChannelId:      "channel-0",
	}); err != nil {
		t.Fatalf("SetDomainChannel failed: %v", err)
	}

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrDomainNotFound) {
		t.Fatalf("expected ErrDomainNotFound, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsCheckpointHashMismatch(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()
	packet.AppHash = []byte("tampered-app-hash")

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrCheckpointHashMismatch) {
		t.Fatalf("expected ErrCheckpointHashMismatch, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsMissingCheckpointProof(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()
	packet.SourceCheckpointProof = nil

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrCheckpointProofRequired) {
		t.Fatalf("expected ErrCheckpointProofRequired, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsInvalidCheckpointProof(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()
	packet.SourceCheckpointProof = []byte("not-the-proof")

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrCheckpointProofInvalid) {
		t.Fatalf("expected ErrCheckpointProofInvalid, got %v", err)
	}
}

func TestReceiveCrossReferencePacketVerifiesHysteresisSignature(t *testing.T) {
	f := initIBCFixture(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	requireIBCBindingWithSourceKey(t, f, publicKey)
	packet := validCrossReferencePacket()
	packet.HysteresisSignature = ed25519.Sign(privateKey, types.HysteresisSignBytes(packetCheckpoint(packet)))

	if _, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet); err != nil {
		t.Fatalf("receiveCrossReferencePacket returned error: %v", err)
	}
}

func TestReceiveCrossReferencePacketVerifiesThresholdHysteresisSignature(t *testing.T) {
	f := initIBCFixture(t)
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 1 failed: %v", err)
	}
	publicKey2, privateKey2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 2 failed: %v", err)
	}
	publicKey3, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 3 failed: %v", err)
	}
	requireIBCBindingWithSourceKey(t, f, types.EncodeThresholdHysteresisPublicKey(2, publicKey1, publicKey2, publicKey3))
	packet := validCrossReferencePacket()
	checkpoint := packetCheckpoint(packet)
	packet.HysteresisSignature = types.EncodeThresholdHysteresisSignature(map[uint8][]byte{
		0: ed25519.Sign(privateKey1, types.HysteresisSignBytes(checkpoint)),
		1: ed25519.Sign(privateKey2, types.HysteresisSignBytes(checkpoint)),
	})

	if _, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet); err != nil {
		t.Fatalf("receiveCrossReferencePacket returned error: %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsBelowThresholdHysteresisSignature(t *testing.T) {
	f := initIBCFixture(t)
	publicKey1, privateKey1, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 1 failed: %v", err)
	}
	publicKey2, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey 2 failed: %v", err)
	}
	requireIBCBindingWithSourceKey(t, f, types.EncodeThresholdHysteresisPublicKey(2, publicKey1, publicKey2))
	packet := validCrossReferencePacket()
	checkpoint := packetCheckpoint(packet)
	packet.HysteresisSignature = types.EncodeThresholdHysteresisSignature(map[uint8][]byte{
		0: ed25519.Sign(privateKey1, types.HysteresisSignBytes(checkpoint)),
	})

	_, err = f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrHysteresisSignatureInvalid) {
		t.Fatalf("expected ErrHysteresisSignatureInvalid, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsInvalidHysteresisSignature(t *testing.T) {
	f := initIBCFixture(t)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	requireIBCBindingWithSourceKey(t, f, publicKey)
	packet := validCrossReferencePacket()
	packet.HysteresisSignature = []byte("invalid-signature")

	_, err = f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrHysteresisSignatureInvalid) {
		t.Fatalf("expected ErrHysteresisSignatureInvalid, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRequiresHysteresisSignatureWhenDomainHasKey(t *testing.T) {
	f := initIBCFixture(t)
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey failed: %v", err)
	}
	requireIBCBindingWithSourceKey(t, f, publicKey)
	packet := validCrossReferencePacket()

	_, err = f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrHysteresisSignatureRequired) {
		t.Fatalf("expected ErrHysteresisSignatureRequired, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsReplay(t *testing.T) {
	f := initIBCFixture(t)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()

	if _, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet); err != nil {
		t.Fatalf("initial receiveCrossReferencePacket returned error: %v", err)
	}
	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrReplayPacket) {
		t.Fatalf("expected ErrReplayPacket, got %v", err)
	}
}

func TestReceiveCrossReferencePacketRejectsStaleCheckpointProof(t *testing.T) {
	f := initIBCFixture(t)
	f.ctx = f.ctx.WithBlockHeight(20050)
	requireIBCBinding(t, f)
	packet := validCrossReferencePacket()

	_, err := f.ibcModule.receiveCrossReferencePacket(f.ctx, types.PortID, "channel-0", "relayer", packet)
	if !errorsmod.IsOf(err, types.ErrCheckpointProofStale) {
		t.Fatalf("expected ErrCheckpointProofStale, got %v", err)
	}
}

func requireIBCBinding(t *testing.T, f *ibcFixture) {
	t.Helper()

	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-a", ChainId: "crossref-a"}); err != nil {
		t.Fatalf("SetDomain source failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-b", ChainId: "crossref-b"}); err != nil {
		t.Fatalf("SetDomain local failed: %v", err)
	}
	if err := f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{
		LocalDomainId:  "chain-b",
		RemoteDomainId: "chain-a",
		PortId:         types.PortID,
		ChannelId:      "channel-0",
		ClientId:       "client-a",
	}); err != nil {
		t.Fatalf("SetDomainChannel failed: %v", err)
	}
}

func requireIBCBindingWithSourceKey(t *testing.T, f *ibcFixture, sourceKey []byte) {
	t.Helper()

	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{
		DomainId:            "chain-a",
		ChainId:             "crossref-a",
		HysteresisPublicKey: sourceKey,
	}); err != nil {
		t.Fatalf("SetDomain source failed: %v", err)
	}
	if err := f.keeper.SetDomain(f.ctx, types.DomainInfo{DomainId: "chain-b", ChainId: "crossref-b"}); err != nil {
		t.Fatalf("SetDomain local failed: %v", err)
	}
	if err := f.keeper.SetDomainChannel(f.ctx, types.DomainChannel{
		LocalDomainId:  "chain-b",
		RemoteDomainId: "chain-a",
		PortId:         types.PortID,
		ChannelId:      "channel-0",
		ClientId:       "client-a",
	}); err != nil {
		t.Fatalf("SetDomainChannel failed: %v", err)
	}
}

func validCrossReferencePacket() *types.CrossReferencePacketData {
	checkpoint := types.Checkpoint{
		DomainId:         "chain-a",
		Height:           1,
		BlockHash:        []byte("block-hash-a-1"),
		AppHash:          []byte("app-hash-a-1"),
		ValidatorSetHash: []byte("validator-set-a"),
		BlockTimeUnix:    1700000000,
	}
	checkpoint.CheckpointHash = types.ComputeCheckpointHash(checkpoint)

	return &types.CrossReferencePacketData{
		SourceDomainId:            checkpoint.DomainId,
		SourceChainId:             "crossref-a",
		SourceHeight:              checkpoint.Height,
		BlockHash:                 checkpoint.BlockHash,
		AppHash:                   checkpoint.AppHash,
		ValidatorSetHash:          checkpoint.ValidatorSetHash,
		PreviousCheckpointHash:    checkpoint.PreviousCheckpointHash,
		CheckpointHash:            checkpoint.CheckpointHash,
		HysteresisSignature:       checkpoint.HysteresisSignature,
		BlockTimeUnix:             checkpoint.BlockTimeUnix,
		SourceCheckpointProof:     []byte("proof-a-1"),
		SourceProofRevisionNumber: 1,
		SourceProofRevisionHeight: 10,
	}
}

func packetCheckpoint(packet *types.CrossReferencePacketData) types.Checkpoint {
	return types.Checkpoint{
		DomainId:               packet.SourceDomainId,
		Height:                 packet.SourceHeight,
		BlockHash:              packet.BlockHash,
		AppHash:                packet.AppHash,
		ValidatorSetHash:       packet.ValidatorSetHash,
		PreviousCheckpointHash: packet.PreviousCheckpointHash,
		CheckpointHash:         packet.CheckpointHash,
		HysteresisSignature:    packet.HysteresisSignature,
		BlockTimeUnix:          packet.BlockTimeUnix,
	}
}
