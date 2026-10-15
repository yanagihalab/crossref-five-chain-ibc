// Verifies a saved local pilot proof against its RPC header, not a trust root.
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	clienttypes "github.com/cosmos/ibc-go/v11/modules/core/02-client/types"
	commitmenttypes "github.com/cosmos/ibc-go/v11/modules/core/23-commitment/types"
	"github.com/crossref/crossrefd/x/crossref/types"
)

type headerBlock struct {
	Block struct {
		Header struct {
			ChainID string `json:"chain_id"`
			Height  string `json:"height"`
			AppHash string `json:"app_hash"`
		} `json:"header"`
	} `json:"block"`
}

type bundle struct {
	ChainID    string          `json:"chainId"`
	PublicKey  []byte          `json:"publicKey"`
	Checkpoint json.RawMessage `json:"checkpoint"`
	Proof      struct {
		DomainID   string `json:"domain_id"`
		Height     uint64 `json:"height"`
		StoreKey   []byte `json:"store_key"`
		Bytes      []byte `json:"source_checkpoint_proof"`
		Revision   uint64 `json:"source_proof_revision_number"`
		HeightRoot uint64 `json:"source_proof_revision_height"`
	} `json:"proof"`
	ProofBlock headerBlock `json:"proofBlock"`
}

func verify(b bundle) error {
	cdc := codec.NewProtoCodec(codectypes.NewInterfaceRegistry())
	var cp types.Checkpoint
	if err := cdc.UnmarshalJSON(b.Checkpoint, &cp); err != nil {
		return err
	}
	if len(b.PublicKey) != 32 {
		return fmt.Errorf("public key required")
	}
	if err := types.NormalizeCheckpointHashes(&cp); err != nil {
		return err
	}
	if err := types.VerifyHysteresisSignature(types.DomainInfo{DomainId: cp.DomainId, HysteresisPublicKey: b.PublicKey, KeyEpoch: 1}, cp); err != nil {
		return err
	}
	header := b.ProofBlock.Block.Header
	if header.ChainID != b.ChainID || header.Height != strconv.FormatUint(b.Proof.HeightRoot, 10) || b.Proof.Revision != clienttypes.ParseChainID(b.ChainID) || cp.DomainId != b.Proof.DomainID || cp.Height != b.Proof.Height {
		return fmt.Errorf("proof domain/height/chain mismatch")
	}
	root, err := hex.DecodeString(header.AppHash)
	if err != nil || len(root) != 32 {
		return fmt.Errorf("invalid app hash")
	}
	key, err := types.CheckpointStorageKey(cp.DomainId, cp.Height)
	if err != nil {
		return err
	}
	if !bytes.Equal(key, b.Proof.StoreKey) {
		return fmt.Errorf("store key mismatch")
	}
	value, err := codec.CollValue[types.Checkpoint](cdc).Encode(cp)
	if err != nil {
		return err
	}
	var proof commitmenttypes.MerkleProof
	if err := proof.Unmarshal(b.Proof.Bytes); err != nil {
		return err
	}
	path := commitmenttypes.NewMerklePath([]byte(types.StoreKey), key)
	if err := proof.VerifyMembership(commitmenttypes.GetSDKSpecs(), commitmenttypes.NewMerkleRoot(root), path, value); err != nil {
		return err
	}
	wrong := bytes.Clone(root)
	wrong[0] ^= 1
	if proof.VerifyMembership(commitmenttypes.GetSDKSpecs(), commitmenttypes.NewMerkleRoot(wrong), path, value) == nil {
		return fmt.Errorf("wrong root accepted")
	}
	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: local-proof-check BUNDLE.json")
		os.Exit(1)
	}
	data, err := os.ReadFile(os.Args[1])
	var b bundle
	if err == nil {
		err = json.Unmarshal(data, &b)
	}
	if err == nil {
		err = verify(b)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]bool{"membershipVerified": true, "wrongRootRejected": true, "signatureVerified": true})
}
