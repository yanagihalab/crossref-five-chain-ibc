// Package historyproof supplies explicitly synthetic headers and keys for tests.
package historyproof

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"time"

	cmtversion "github.com/cometbft/cometbft/proto/tendermint/version"
	cmt "github.com/cometbft/cometbft/types"
	"github.com/cometbft/cometbft/version"
	"github.com/crossref/crossrefd/x/crossref/types"
)

func Key(domain string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte("ancestry-test-" + domain))
	return ed25519.NewKeyFromSeed(seed[:])
}
func Config(local string) types.HistoryConfig {
	c := types.HistoryConfig{Enabled: true, SessionId: "ancestry-fixture", LocalDomainId: local, IntervalBlocks: 3, PacketTimeoutSeconds: 60, RequireAncestryProof: true}
	for _, id := range []string{"a", "b", "c"} {
		c.Members = append(c.Members, types.HistoryMember{DomainId: id, ChainId: id, PublicKey: Key(id).Public().(ed25519.PublicKey)})
	}
	return c
}
func Headers(chain string, count int) []cmt.Header {
	var headers []cmt.Header
	for i := 0; i < count; i++ {
		h := cmt.Header{Version: cmtversion.Consensus{Block: version.BlockProtocol}, ChainID: chain, Height: int64(i + 1), Time: time.Unix(1800000000+int64(i*6), 0).UTC(), ValidatorsHash: bytes.Repeat([]byte{1}, 32), NextValidatorsHash: bytes.Repeat([]byte{1}, 32), AppHash: bytes.Repeat([]byte{byte(i + 1)}, 32), ProposerAddress: bytes.Repeat([]byte{2}, 20)}
		if i > 0 {
			h.LastBlockID = cmt.BlockID{Hash: headers[i-1].Hash(), PartSetHeader: cmt.PartSetHeader{Total: 1, Hash: bytes.Repeat([]byte{3}, 32)}}
		}
		headers = append(headers, h)
	}
	return headers
}
func Part(domain string, headers []cmt.Header, start, height int) types.HistoryPart {
	p := types.HistoryPart{DomainId: domain, ChainId: headers[0].ChainID, Height: uint64(height), BlockHash: headers[height-1].Hash(), AppHash: headers[height].AppHash}
	proof := types.HistoryAncestryProof{}
	for _, h := range headers[start-1 : height+1] {
		pb := h.ToProto()
		raw, err := pb.Marshal()
		if err != nil {
			panic(err)
		}
		proof.Headers = append(proof.Headers, raw)
	}
	var err error
	p.AncestryProof, err = proof.Marshal()
	if err != nil {
		panic(err)
	}
	return p
}
func Round(c types.HistoryConfig, number uint64, height int, previous types.HistoryRound) types.HistoryRound {
	r := types.HistoryRound{Number: number, OpenedHeight: uint64(height + 1), CompletedHeight: uint64(height + 3), Status: types.HistoryCompleted}
	for _, m := range c.Members {
		start := height
		if old := types.HistoryAnchor(previous, m.DomainId); old != nil {
			start = int(old.Height)
		}
		r.Parts = append(r.Parts, Part(m.DomainId, Headers(m.ChainId, height+1), start, height))
	}
	for _, m := range c.Members {
		b, err := types.HistorySignBytes(c, r, previous, m.DomainId)
		if err != nil {
			panic(err)
		}
		r.Seals = append(r.Seals, types.HistorySeal{DomainId: m.DomainId, Signature: ed25519.Sign(Key(m.DomainId), b), PreviousSignatureHash: types.HistoryPreviousSignatureHash(previous, m.DomainId), RecordedHeight: uint64(height + 2)})
	}
	return r
}
