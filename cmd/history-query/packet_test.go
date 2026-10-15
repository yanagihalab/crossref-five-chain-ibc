package main

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/crossref/crossrefd/x/crossref/types"
	"github.com/stretchr/testify/require"
)

func TestPacketStdinAvoidsArgumentLimitAndBoundsInput(t *testing.T) {
	wire := types.CrossrefPacketData{Packet: &types.CrossrefPacketData_History{History: &types.HistoryPacketData{
		Round: 2, SourceDomainId: "chain-a", Part: &types.HistoryPart{AncestryProof: make([]byte, types.MaxHistoryProofBytes)},
	}}}
	bz, err := wire.Marshal()
	require.NoError(t, err)
	require.Greater(t, len(bz), 256*1024)
	metadata, err := decodePacket("", bytes.NewReader(bz))
	require.NoError(t, err)
	require.Equal(t, map[string]any{"round": uint64(2), "source": "chain-a", "kind": "part"}, metadata)
	legacy, err := decodePacket(hex.EncodeToString(bz), nil)
	require.NoError(t, err)
	require.Equal(t, metadata, legacy)
	for _, bad := range [][]byte{nil, {0xff}, make([]byte, types.MaxHistoryPacketBytes+1)} {
		_, err = decodePacket("", bytes.NewReader(bad))
		require.Error(t, err)
	}
	_, err = decodePacket("ff", bytes.NewReader(bz))
	require.Error(t, err)
	_, err = decodePacket("not-hex", nil)
	require.Error(t, err)
}
