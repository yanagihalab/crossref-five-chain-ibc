package module

import (
	"cosmossdk.io/core/address"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/core/store"
	"cosmossdk.io/depinject"
	"cosmossdk.io/depinject/appconfig"
	"github.com/cosmos/cosmos-sdk/codec"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/crossref/crossrefd/x/sanction/keeper"
	"github.com/crossref/crossrefd/x/sanction/types"
)

var _ depinject.OnePerModuleType = AppModule{}

func init() { appconfig.Register(&types.Module{}, appconfig.Provide(ProvideModule)) }

type ModuleInputs struct {
	depinject.In
	Config       *types.Module
	StoreService store.KVStoreService
	Cdc          codec.Codec
	AddressCodec address.Codec
	BankKeeper   types.BankKeeper
}
type ModuleOutputs struct {
	depinject.Out
	SanctionKeeper keeper.Keeper
	Module         appmodule.AppModule
}

func ProvideModule(in ModuleInputs) ModuleOutputs {
	authority := authtypes.NewModuleAddress(types.GovModuleName)
	if in.Config.Authority != "" {
		authority = authtypes.NewModuleAddressOrBech32Address(in.Config.Authority)
	}
	k := keeper.NewKeeper(in.StoreService, in.Cdc, in.AddressCodec, authority, in.BankKeeper)
	return ModuleOutputs{SanctionKeeper: k, Module: NewAppModule(in.Cdc, k)}
}
