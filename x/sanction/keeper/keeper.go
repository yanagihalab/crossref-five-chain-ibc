package keeper

import (
	"cosmossdk.io/collections"
	"cosmossdk.io/core/address"
	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	"fmt"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/crossref/crossrefd/x/sanction/types"
)

type Keeper struct {
	storeService      corestore.KVStoreService
	cdc               codec.Codec
	addressCodec      address.Codec
	authority         []byte
	bankKeeper        types.BankKeeper
	Schema            collections.Schema
	Params            collections.Item[types.Params]
	Agents            collections.Map[string, types.AgentInfo]
	AgentBySigners    collections.Map[string, string]
	RiskReports       collections.Map[string, types.RiskReport]
	SanctionCases     collections.Map[string, types.SanctionCase]
	SanctionVotes     collections.Map[string, types.SanctionVote]
	ActiveTxSanctions collections.Map[string, types.ActiveSanction]
	FrozenAddresses   collections.Map[string, types.FreezeRecord]
	ExecutionRecords  collections.Map[string, types.ExecutionRecord]
}

func NewKeeper(storeService corestore.KVStoreService, cdc codec.Codec, addressCodec address.Codec, authority []byte, bankKeeper types.BankKeeper) Keeper {
	if _, err := addressCodec.BytesToString(authority); err != nil {
		panic(fmt.Sprintf("invalid authority address %s: %s", authority, err))
	}
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{storeService: storeService, cdc: cdc, addressCodec: addressCodec, authority: authority, bankKeeper: bankKeeper, Params: collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)), Agents: collections.NewMap(sb, types.AgentKeyPrefix, "agents", collections.StringKey, codec.CollValue[types.AgentInfo](cdc)), AgentBySigners: collections.NewMap(sb, types.AgentBySignerKeyPrefix, "agent_by_signers", collections.StringKey, collections.StringValue), RiskReports: collections.NewMap(sb, types.RiskReportKeyPrefix, "risk_reports", collections.StringKey, codec.CollValue[types.RiskReport](cdc)), SanctionCases: collections.NewMap(sb, types.SanctionCaseKeyPrefix, "sanction_cases", collections.StringKey, codec.CollValue[types.SanctionCase](cdc)), SanctionVotes: collections.NewMap(sb, types.SanctionVoteKeyPrefix, "sanction_votes", collections.StringKey, codec.CollValue[types.SanctionVote](cdc)), ActiveTxSanctions: collections.NewMap(sb, types.ActiveTxSanctionKeyPrefix, "active_tx_sanctions", collections.StringKey, codec.CollValue[types.ActiveSanction](cdc)), FrozenAddresses: collections.NewMap(sb, types.FrozenAddressKeyPrefix, "frozen_addresses", collections.StringKey, codec.CollValue[types.FreezeRecord](cdc)), ExecutionRecords: collections.NewMap(sb, types.ExecutionRecordKeyPrefix, "execution_records", collections.StringKey, codec.CollValue[types.ExecutionRecord](cdc))}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}
func (k Keeper) GetAuthority() []byte { return k.authority }
func (k Keeper) AuthorityString() string {
	s, err := k.addressCodec.BytesToString(k.authority)
	if err != nil {
		panic(err)
	}
	return s
}
func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", types.ModuleName)
}
