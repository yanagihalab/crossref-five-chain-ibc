package app

import (
	"reflect"
	"testing"

	log "cosmossdk.io/log/v2"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	simtestutil "github.com/cosmos/cosmos-sdk/testutil/sims"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	upgradetypes "github.com/cosmos/cosmos-sdk/x/upgrade/types"
	icatypes "github.com/cosmos/ibc-go/v11/modules/apps/27-interchain-accounts/types"
	ibctransfertypes "github.com/cosmos/ibc-go/v11/modules/apps/transfer/types"
	ibcexported "github.com/cosmos/ibc-go/v11/modules/core/exported"
	ibctm "github.com/cosmos/ibc-go/v11/modules/light-clients/07-tendermint"
	"github.com/stretchr/testify/require"

	crossreftypes "github.com/crossref/crossrefd/x/crossref/types"
	sanctiontypes "github.com/crossref/crossrefd/x/sanction/types"
)

func TestCrossrefIBCAppWiring(t *testing.T) {
	appOptions := make(simtestutil.AppOptionsMap, 0)
	appOptions[flags.FlagHome] = DefaultNodeHome

	app := New(log.NewNopLogger(), dbm.NewMemDB(), nil, false, appOptions, baseapp.SetChainID("crossref-wiring-test"))

	require.NotNil(t, app.IBCKeeper)
	require.NotNil(t, app.CrossrefKeeper)
	require.NotNil(t, app.GovKeeper)
	require.NotNil(t, app.UpgradeKeeper)
	require.Contains(t, app.DefaultGenesis(), ibcexported.ModuleName)
	require.Contains(t, app.DefaultGenesis(), crossreftypes.ModuleName)
	require.Contains(t, app.DefaultGenesis(), sanctiontypes.ModuleName)
	require.Contains(t, app.DefaultGenesis(), govtypes.ModuleName)
	require.Contains(t, app.DefaultGenesis(), upgradetypes.ModuleName)
	require.False(t, reflect.ValueOf(app.CrossrefKeeper).FieldByName("ibcKeeperFn").IsNil(), "crossref keeper must be wired to the app IBC keeper")
}

func TestOperationalGovernanceUpgradeAndIBCModulesAreWired(t *testing.T) {
	appOptions := make(simtestutil.AppOptionsMap, 0)
	appOptions[flags.FlagHome] = DefaultNodeHome

	app := New(log.NewNopLogger(), dbm.NewMemDB(), nil, false, appOptions, baseapp.SetChainID("crossref-operational-wiring-test"))
	versionMap := app.ModuleManager.GetVersionMap()

	require.Contains(t, versionMap, govtypes.ModuleName, "governance must be present for MsgUpdateParams proposals")
	require.Contains(t, versionMap, upgradetypes.ModuleName, "upgrade module must be present for upgrade plans and migrations")
	require.Contains(t, versionMap, ibcexported.ModuleName, "IBC core must be migrated with the app")
	require.Contains(t, versionMap, ibctransfertypes.ModuleName)
	require.Contains(t, versionMap, icatypes.ModuleName)
	require.Contains(t, versionMap, ibctm.ModuleName)
	require.Contains(t, versionMap, crossreftypes.ModuleName)
	require.Contains(t, versionMap, sanctiontypes.ModuleName)

	require.NotNil(t, app.IBCKeeper)
	require.NotNil(t, app.IBCKeeper.ChannelKeeper)
	require.NotNil(t, app.IBCKeeper.PortKeeper)
	require.NotNil(t, app.CrossrefKeeper)
	require.NotNil(t, app.UpgradeKeeper)
}
