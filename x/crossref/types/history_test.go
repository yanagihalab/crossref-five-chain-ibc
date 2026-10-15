package types

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"github.com/cosmos/gogoproto/proto"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHistoryOutOfOrderSealsAndFutureParts(t *testing.T) {
	c := HistoryConfig{Enabled: true, SessionId: "reorder", IntervalBlocks: 2, PacketTimeoutSeconds: 60}
	keys := map[string]ed25519.PrivateKey{}
	for _, id := range []string{"a", "b", "c"} {
		h := sha256.Sum256([]byte(id))
		keys[id] = ed25519.NewKeyFromSeed(h[:])
		c.Members = append(c.Members, HistoryMember{DomainId: id, ChainId: id, PublicKey: keys[id].Public().(ed25519.PublicKey)})
	}
	nodes := map[string]*HistoryState{}
	hash := bytes.Repeat([]byte{1}, 32)
	for _, m := range c.Members {
		local := c
		local.LocalDomainId = m.DomainId
		s := &HistoryState{Config: local}
		_, err := s.BeginHistory(10, hash, hash)
		require.NoError(t, err)
		nodes[m.DomainId] = s
	}
	part := func(id string, round uint64) *HistoryPacketData {
		for _, o := range nodes[id].Outbox {
			if o.Packet.Part != nil && o.Packet.Round == round {
				return proto.Clone(&o.Packet).(*HistoryPacketData)
			}
		}
		t.Fatal("missing part")
		return nil
	}
	for _, source := range []string{"a", "b", "c"} {
		for _, dest := range []string{"a", "b", "c"} {
			if source == dest || source == "c" && dest == "a" {
				continue
			}
			require.NoError(t, nodes[dest].ReceiveHistory(*part(source, 1), 10))
		}
	}
	for _, id := range []string{"b", "c"} {
		s := nodes[id]
		b, err := HistorySignBytes(s.Config, s.Current, s.Previous, id)
		require.NoError(t, err)
		require.NoError(t, s.SealHistory(1, 11, ed25519.Sign(keys[id], b)))
	}
	seal := func(id string) *HistoryPacketData {
		for _, o := range nodes[id].Outbox {
			if o.Packet.Seal != nil {
				return proto.Clone(&o.Packet).(*HistoryPacketData)
			}
		}
		t.Fatal("missing seal")
		return nil
	}
	require.NoError(t, nodes["a"].ReceiveHistory(*seal("b"), 11))
	require.Equal(t, HistoryCollecting, nodes["a"].Current.Status)
	require.NoError(t, nodes["a"].ReceiveHistory(*part("c", 1), 11))
	require.Equal(t, HistorySigning, nodes["a"].Current.Status)
	b, err := HistorySignBytes(nodes["a"].Config, nodes["a"].Current, nodes["a"].Previous, "a")
	require.NoError(t, err)
	require.NoError(t, nodes["a"].SealHistory(1, 12, ed25519.Sign(keys["a"], b)))
	for _, source := range []string{"a", "b", "c"} {
		for _, dest := range []string{"a", "b", "c"} {
			if source != dest {
				require.NoError(t, nodes[dest].ReceiveHistory(*seal(source), 13))
			}
		}
	}
	oldPart := part("b", 1)
	for _, s := range nodes {
		require.Equal(t, HistoryCompleted, s.Current.Status)
		s.Outbox = nil
	}
	_, err = nodes["b"].BeginHistory(20, hash, hash)
	require.NoError(t, err)
	future := part("b", 2)
	require.NoError(t, nodes["a"].ReceiveHistory(*future, 14))
	require.Len(t, nodes["a"].PendingParts, 1)
	conflicting := proto.Clone(future).(*HistoryPacketData)
	conflicting.Part.Height++
	require.Error(t, nodes["a"].ReceiveHistory(*conflicting, 14))
	_, err = nodes["a"].BeginHistory(20, hash, hash)
	require.NoError(t, err)
	require.Empty(t, nodes["a"].PendingParts)
	require.Len(t, nodes["a"].Current.Parts, 2)
	require.NoError(t, nodes["a"].ReceiveHistory(*oldPart, 20))
	stale := proto.Clone(future).(*HistoryPacketData)
	stale.SourceDomainId = "c"
	stale.Part.DomainId = "c"
	stale.Part.ChainId = "c"
	stale.Part.Height = 11
	require.ErrorContains(t, nodes["a"].ReceiveHistory(*stale, 20), "previous history signature")
	stale.Part.Height = 19
	require.NoError(t, nodes["a"].ReceiveHistory(*stale, 20))
	wrongPrev := proto.Clone(nodes["a"]).(*HistoryState)
	wrongPrev.Previous.Seals[0].Signature = []byte("wrong")
	good, err := HistorySignBytes(nodes["a"].Config, nodes["a"].Current, nodes["a"].Previous, "a")
	require.NoError(t, err)
	require.Error(t, wrongPrev.SealHistory(2, 21, ed25519.Sign(keys["a"], good)))
}
