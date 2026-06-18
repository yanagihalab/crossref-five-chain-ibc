package app

import (
	"reflect"
	"testing"

	log "cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"
	"github.com/stretchr/testify/require"
)

func TestCrossrefIBCAppWiring(t *testing.T) {
	appOptions := make(simtestutil.AppOptionsMap, 0)
	appOptions[flags.FlagHome] = DefaultNodeHome

	app := New(log.NewNopLogger(), dbm.NewMemDB(), nil, false, appOptions, baseapp.SetChainID("crossref-wiring-test"))

	require.NotNil(t, app.IBCKeeper)
	require.NotNil(t, app.CrossrefKeeper)
	require.Contains(t, app.DefaultGenesis(), ibcexported.ModuleName)
	require.Contains(t, app.DefaultGenesis(), "crossref")
	require.False(t, reflect.ValueOf(app.CrossrefKeeper).FieldByName("ibcKeeperFn").IsNil(), "crossref keeper must be wired to the app IBC keeper")
}
