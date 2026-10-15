package keeper_test

import (
	"bytes"
	"testing"

	errorsmod "cosmossdk.io/errors"

	"github.com/crossref/crossrefd/x/crossref/types"
)

func TestUpdateTrustedLightClientInitializesFromTrustRoot(t *testing.T) {
	f := initFixture(t)
	rootHash := []byte("validators-root")
	nextHash := []byte("validators-next")
	domain := types.DomainInfo{
		DomainId:         "chain-a",
		ChainId:          "crossref-a",
		ValidatorSetHash: rootHash,
	}

	err := f.keeper.UpdateTrustedLightClient(f.ctx, domain, types.Checkpoint{DomainId: "chain-a", Height: 10}, types.VerifiedCometBFTConsensusProof{
		ChainID:              "crossref-a",
		Height:               10,
		BlockHash:            []byte("block-10"),
		AppHash:              []byte("app-10"),
		ValidatorSetHash:     rootHash,
		NextValidatorSetHash: nextHash,
		BlockTimeUnix:        1700000000,
	})
	if err != nil {
		t.Fatalf("UpdateTrustedLightClient returned error: %v", err)
	}

	state, found, err := f.keeper.GetTrustedLightClientState(f.ctx, "chain-a")
	if err != nil {
		t.Fatalf("GetTrustedLightClientState returned error: %v", err)
	}
	if !found {
		t.Fatal("expected trusted light client state")
	}
	if state.LatestHeight != 10 || !bytes.Equal(state.LatestValidatorSetHash, rootHash) || !bytes.Equal(state.NextValidatorSetHash, nextHash) {
		t.Fatalf("unexpected trusted state: %+v", state)
	}
	updates, err := f.keeper.ListLightClientUpdates(f.ctx, "chain-a")
	if err != nil {
		t.Fatalf("ListLightClientUpdates returned error: %v", err)
	}
	if len(updates) != 1 || updates[0].VerificationMode != "trust-root" {
		t.Fatalf("unexpected light client updates: %+v", updates)
	}
}

func TestUpdateTrustedLightClientRejectsWrongTrustRoot(t *testing.T) {
	f := initFixture(t)
	err := f.keeper.UpdateTrustedLightClient(f.ctx, types.DomainInfo{
		DomainId:         "chain-a",
		ChainId:          "crossref-a",
		ValidatorSetHash: []byte("trusted-root"),
	}, types.Checkpoint{DomainId: "chain-a", Height: 10}, types.VerifiedCometBFTConsensusProof{
		ChainID:              "crossref-a",
		Height:               10,
		BlockHash:            []byte("block-10"),
		AppHash:              []byte("app-10"),
		ValidatorSetHash:     []byte("untrusted-root"),
		NextValidatorSetHash: []byte("next"),
	})
	if !errorsmod.IsOf(err, types.ErrConsensusProofInvalid) {
		t.Fatalf("expected ErrConsensusProofInvalid, got %v", err)
	}
}

func TestUpdateTrustedLightClientRequiresTrustedNextValidatorSet(t *testing.T) {
	f := initFixture(t)
	rootHash := []byte("validators-root")
	nextHash := []byte("validators-next")
	domain := types.DomainInfo{
		DomainId:         "chain-a",
		ChainId:          "crossref-a",
		ValidatorSetHash: rootHash,
	}

	if err := f.keeper.UpdateTrustedLightClient(f.ctx, domain, types.Checkpoint{DomainId: "chain-a", Height: 10}, types.VerifiedCometBFTConsensusProof{
		ChainID:              "crossref-a",
		Height:               10,
		BlockHash:            []byte("block-10"),
		AppHash:              []byte("app-10"),
		ValidatorSetHash:     rootHash,
		NextValidatorSetHash: nextHash,
	}); err != nil {
		t.Fatalf("initial UpdateTrustedLightClient returned error: %v", err)
	}

	err := f.keeper.UpdateTrustedLightClient(f.ctx, domain, types.Checkpoint{DomainId: "chain-a", Height: 11}, types.VerifiedCometBFTConsensusProof{
		ChainID:              "crossref-a",
		Height:               11,
		BlockHash:            []byte("block-11"),
		AppHash:              []byte("app-11"),
		ValidatorSetHash:     []byte("unexpected-current-validators"),
		NextValidatorSetHash: []byte("next-2"),
	})
	if !errorsmod.IsOf(err, types.ErrConsensusProofInvalid) {
		t.Fatalf("expected ErrConsensusProofInvalid, got %v", err)
	}

	if err := f.keeper.UpdateTrustedLightClient(f.ctx, domain, types.Checkpoint{DomainId: "chain-a", Height: 11}, types.VerifiedCometBFTConsensusProof{
		ChainID:              "crossref-a",
		Height:               11,
		BlockHash:            []byte("block-11"),
		AppHash:              []byte("app-11"),
		ValidatorSetHash:     nextHash,
		NextValidatorSetHash: []byte("next-2"),
	}); err != nil {
		t.Fatalf("trusted transition returned error: %v", err)
	}
}
