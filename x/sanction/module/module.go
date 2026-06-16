package module

import (
	"context"
	"cosmossdk.io/core/appmodule"
	"encoding/json"
	"fmt"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/crossref/crossrefd/x/sanction/keeper"
	"github.com/crossref/crossrefd/x/sanction/types"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var _ module.AppModuleBasic = (*AppModule)(nil)
var _ module.AppModule = (*AppModule)(nil)
var _ module.HasGenesis = (*AppModule)(nil)
var _ appmodule.AppModule = (*AppModule)(nil)

type AppModule struct {
	cdc    codec.Codec
	keeper keeper.Keeper
}

func NewAppModule(cdc codec.Codec, k keeper.Keeper) AppModule       { return AppModule{cdc: cdc, keeper: k} }
func (AppModule) IsAppModule()                                      {}
func (AppModule) IsOnePerModuleType()                               {}
func (AppModule) Name() string                                      { return types.ModuleName }
func (AppModule) RegisterLegacyAminoCodec(*codec.LegacyAmino)       {}
func (AppModule) RegisterInterfaces(r codectypes.InterfaceRegistry) { types.RegisterInterfaces(r) }
func (AppModule) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *runtime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(context.Background(), mux, types.NewQueryClient(clientCtx)); err != nil {
		panic(err)
	}
}
func (am AppModule) RegisterServices(r grpc.ServiceRegistrar) error {
	types.RegisterMsgServer(r, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(r, keeper.NewQueryServerImpl(am.keeper))
	return nil
}
func (am AppModule) DefaultGenesis(codec.JSONCodec) json.RawMessage {
	return am.cdc.MustMarshalJSON(types.DefaultGenesis())
}
func (am AppModule) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}
func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, bz json.RawMessage) {
	var gs types.GenesisState
	if err := am.cdc.UnmarshalJSON(bz, &gs); err != nil {
		panic(err)
	}
	if err := am.keeper.InitGenesis(ctx, gs); err != nil {
		panic(err)
	}
}
func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	gs, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	bz, err := am.cdc.MarshalJSON(gs)
	if err != nil {
		panic(err)
	}
	return bz
}
func (AppModule) ConsensusVersion() uint64    { return 1 }
func (AppModule) GetTxCmd() *cobra.Command    { return &cobra.Command{Use: types.ModuleName} }
func (AppModule) GetQueryCmd() *cobra.Command { return &cobra.Command{Use: types.ModuleName} }
