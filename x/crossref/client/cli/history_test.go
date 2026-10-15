package cli

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHistoryCLIRegistered(t *testing.T) {
	for _, name := range []string{"history", "history-round"} {
		cmd, rest, err := GetQueryCmd().Find([]string{name})
		require.NoError(t, err)
		require.Empty(t, rest)
		require.Equal(t, name, cmd.Name())
		require.NotNil(t, cmd.Flags().Lookup("node"))
		require.NotNil(t, cmd.Flags().Lookup("output"))
	}
	cmd, rest, err := GetTxCmd().Find([]string{"sign-history-round"})
	require.NoError(t, err)
	require.Empty(t, rest)
	require.Equal(t, "sign-history-round", cmd.Name())
	require.NotNil(t, cmd.Flags().Lookup("from"))
	require.Error(t, cmd.Args(cmd, []string{"1"}))
	proof, rest, err := GetTxCmd().Find([]string{"attach-history-proof"})
	require.NoError(t, err)
	require.Empty(t, rest)
	require.Equal(t, "attach-history-proof", proof.Name())
	require.NotNil(t, proof.Flags().Lookup("from"))
	require.Error(t, proof.Args(proof, []string{"1"}))
}
