package keeper_test

import (
	"testing"

	"github.com/crossref/crossrefd/x/crossref/types"

	"github.com/stretchr/testify/require"
)

func TestGenesis(t *testing.T) {
	genesisState := types.GenesisState{
		Params: types.DefaultParams(),
		PortId: types.PortID,
		TrustedLightClientStates: []types.TrustedLightClientState{
			{
				DomainId:               "chain-a",
				ChainId:                "crossref-a",
				LatestHeight:           7,
				LatestBlockHash:        []byte("block-7"),
				LatestValidatorSetHash: []byte("validators-7"),
				NextValidatorSetHash:   []byte("validators-8"),
			},
		},
		LightClientUpdates: []types.LightClientUpdate{
			{
				DomainId:                "chain-a",
				ChainId:                 "crossref-a",
				TargetHeight:            7,
				TargetBlockHash:         []byte("block-7"),
				TargetValidatorSetHash:  []byte("validators-7"),
				NextValidatorSetHash:    []byte("validators-8"),
				VerificationMode:        "trust-root",
				TrustedValidatorSetHash: []byte("validators-7"),
			},
		},
	}

	f := initFixture(t)
	err := f.keeper.InitGenesis(f.ctx, genesisState)
	require.NoError(t, err)
	got, err := f.keeper.ExportGenesis(f.ctx)
	require.NoError(t, err)
	require.NotNil(t, got)

	require.Equal(t, genesisState.PortId, got.PortId)
	require.EqualExportedValues(t, genesisState.Params, got.Params)
	require.EqualExportedValues(t, genesisState.TrustedLightClientStates, got.TrustedLightClientStates)
	require.EqualExportedValues(t, genesisState.LightClientUpdates, got.LightClientUpdates)
}
