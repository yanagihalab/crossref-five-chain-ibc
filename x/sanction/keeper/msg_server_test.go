package keeper

import (
	"context"
	"testing"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	"github.com/cosmos/cosmos-sdk/runtime"
	storetypes "github.com/cosmos/cosmos-sdk/store/v2/types"
	"github.com/cosmos/cosmos-sdk/testutil"
	sdk "github.com/cosmos/cosmos-sdk/types"
	moduletestutil "github.com/cosmos/cosmos-sdk/types/module/testutil"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	govtypes "github.com/cosmos/cosmos-sdk/x/gov/types"
	"github.com/stretchr/testify/require"

	"github.com/crossref/crossrefd/x/sanction/types"
)

type sanctionFixture struct {
	ctx          context.Context
	keeper       Keeper
	msgServer    types.MsgServer
	authority    string
	agentSigner1 string
	agentSigner2 string
}

type mockBankKeeper struct{}

func (mockBankKeeper) SpendableCoins(context.Context, sdk.AccAddress) sdk.Coins {
	return sdk.NewCoins()
}

func (mockBankKeeper) SendCoins(context.Context, sdk.AccAddress, sdk.AccAddress, sdk.Coins) error {
	return nil
}

func (mockBankKeeper) SendCoinsFromAccountToModule(context.Context, sdk.AccAddress, string, sdk.Coins) error {
	return nil
}

func initSanctionFixture(t *testing.T) *sanctionFixture {
	t.Helper()

	encCfg := moduletestutil.MakeTestEncodingConfig()
	types.RegisterInterfaces(encCfg.InterfaceRegistry)

	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	storeService := runtime.NewKVStoreService(storeKey)
	ctx := testutil.DefaultContextWithDB(t, storeKey, storetypes.NewTransientStoreKey("transient_test")).Ctx.WithBlockHeight(10)
	addressCodec := addresscodec.NewBech32Codec("crossref")
	authority := authtypes.NewModuleAddress(govtypes.ModuleName)

	k := NewKeeper(storeService, encCfg.Codec, addressCodec, authority, mockBankKeeper{})
	require.NoError(t, k.Params.Set(ctx, types.DefaultParams()))

	authorityString, err := addressCodec.BytesToString(authority)
	require.NoError(t, err)
	agentSigner1, err := addressCodec.BytesToString(sdk.AccAddress("agent-signer-1--"))
	require.NoError(t, err)
	agentSigner2, err := addressCodec.BytesToString(sdk.AccAddress("agent-signer-2--"))
	require.NoError(t, err)

	return &sanctionFixture{
		ctx:          ctx,
		keeper:       k,
		msgServer:    NewMsgServerImpl(k),
		authority:    authorityString,
		agentSigner1: agentSigner1,
		agentSigner2: agentSigner2,
	}
}

func (f *sanctionFixture) registerAgent(t *testing.T, id, signer string) {
	t.Helper()
	_, err := f.msgServer.RegisterAgent(f.ctx, &types.MsgRegisterAgent{
		Authority: f.authority,
		Agent: types.AgentInfo{
			AgentId:         id,
			SignerAddress:   signer,
			Active:          true,
			LlmEndpointHash: "test-agent-" + id,
		},
	})
	require.NoError(t, err)
}

func (f *sanctionFixture) submitRiskReport(t *testing.T, id string, score uint32, target types.SanctionTargetType) []byte {
	t.Helper()
	txHash := TxHash([]byte("tx-" + id))
	_, err := f.msgServer.SubmitRiskReport(f.ctx, &types.MsgSubmitRiskReport{
		Submitter: f.agentSigner1,
		Report: types.RiskReport{
			ReportId:       id,
			TxHash:         txHash,
			FromAddress:    f.agentSigner1,
			ToAddress:      f.agentSigner2,
			Amount:         "10",
			Denom:          "stake",
			RiskScore:      score,
			Categories:     []string{"fraud"},
			Source:         "mock-risk-service",
			ObservedHeight: 10,
			ExpiryHeight:   100,
			EvidenceHash:   EvidenceHashStrings(id, "evidence"),
			Submitter:      f.agentSigner1,
		},
	})
	require.NoError(t, err)
	return txHash
}

func TestSanctionBlocksTxAfterApprovedCase(t *testing.T) {
	f := initSanctionFixture(t)
	f.registerAgent(t, "agent-1", f.agentSigner1)
	f.registerAgent(t, "agent-2", f.agentSigner2)
	txHash := f.submitRiskReport(t, "risk-1", 75, types.SanctionTargetType_SANCTION_TARGET_TYPE_TX)

	_, err := f.msgServer.OpenSanctionCase(f.ctx, &types.MsgOpenSanctionCase{
		Submitter:       f.agentSigner1,
		CaseId:          "case-1",
		TxHash:          txHash,
		TargetType:      types.SanctionTargetType_SANCTION_TARGET_TYPE_TX,
		RequestedAction: types.SanctionAction_SANCTION_ACTION_BLOCK_TX,
		ReportIds:       []string{"risk-1"},
		PolicyVersion:   "test-policy-v1",
	})
	require.NoError(t, err)

	vote := types.SanctionVote{
		CaseId:         "case-1",
		AgentId:        "agent-1",
		Option:         types.VoteOption_VOTE_OPTION_APPROVE,
		ApprovedAction: types.SanctionAction_SANCTION_ACTION_BLOCK_TX,
		SignedHeight:   11,
	}
	_, err = f.msgServer.SubmitSanctionVote(f.ctx, &types.MsgSubmitSanctionVote{Submitter: f.agentSigner1, Vote: vote})
	require.NoError(t, err)
	_, err = f.msgServer.SubmitSanctionVote(f.ctx, &types.MsgSubmitSanctionVote{Submitter: f.agentSigner1, Vote: vote})
	require.ErrorIs(t, err, types.ErrVoteAlreadySubmitted)

	_, err = f.msgServer.SubmitSanctionVote(f.ctx, &types.MsgSubmitSanctionVote{
		Submitter: f.agentSigner2,
		Vote: types.SanctionVote{
			CaseId:         "case-1",
			AgentId:        "agent-2",
			Option:         types.VoteOption_VOTE_OPTION_APPROVE,
			ApprovedAction: types.SanctionAction_SANCTION_ACTION_BLOCK_TX,
			SignedHeight:   12,
		},
	})
	require.NoError(t, err)

	_, err = f.msgServer.ExecuteSanction(f.ctx, &types.MsgExecuteSanction{
		Executor:      f.agentSigner1,
		CaseId:        "case-1",
		ResultMessage: "blocked by approved risk reports",
	})
	require.NoError(t, err)

	contains, err := f.keeper.ContainsSanctionedTx(f.ctx, [][]byte{[]byte("tx-risk-1")})
	require.NoError(t, err)
	require.True(t, contains)

	filtered, err := f.keeper.FilterSanctionedTxs(f.ctx, [][]byte{[]byte("tx-risk-1"), []byte("clean-tx")})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("clean-tx")}, filtered)
}

func TestSanctionFreezesAddressAfterApprovedCase(t *testing.T) {
	f := initSanctionFixture(t)
	f.registerAgent(t, "agent-1", f.agentSigner1)
	txHash := f.submitRiskReport(t, "risk-2", 85, types.SanctionTargetType_SANCTION_TARGET_TYPE_ADDRESS)

	_, err := f.msgServer.OpenSanctionCase(f.ctx, &types.MsgOpenSanctionCase{
		Submitter:       f.agentSigner1,
		CaseId:          "case-2",
		TxHash:          txHash,
		TargetAddress:   f.agentSigner2,
		TargetType:      types.SanctionTargetType_SANCTION_TARGET_TYPE_ADDRESS,
		RequestedAction: types.SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS,
		ReportIds:       []string{"risk-2"},
		PolicyVersion:   "test-policy-v1",
	})
	require.NoError(t, err)
	_, err = f.msgServer.SubmitSanctionVote(f.ctx, &types.MsgSubmitSanctionVote{
		Submitter: f.agentSigner1,
		Vote: types.SanctionVote{
			CaseId:         "case-2",
			AgentId:        "agent-1",
			Option:         types.VoteOption_VOTE_OPTION_APPROVE,
			ApprovedAction: types.SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS,
			SignedHeight:   11,
		},
	})
	require.NoError(t, err)

	_, err = f.msgServer.ExecuteSanction(f.ctx, &types.MsgExecuteSanction{
		Executor:      f.agentSigner1,
		CaseId:        "case-2",
		ResultMessage: "freeze target",
	})
	require.NoError(t, err)
	require.True(t, f.keeper.IsAddressFrozen(f.ctx, f.agentSigner2))
	require.ErrorIs(t, f.keeper.SendCoinsAllowed(f.ctx, f.agentSigner1, f.agentSigner2), types.ErrAddressAlreadyFrozen)

	_, err = f.msgServer.RevokeSanction(f.ctx, &types.MsgRevokeSanction{Authority: f.authority, CaseId: "case-2"})
	require.NoError(t, err)
	require.False(t, f.keeper.IsAddressFrozen(f.ctx, f.agentSigner2))
}
