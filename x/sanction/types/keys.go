package types

import "cosmossdk.io/collections"

const (
	ModuleName = "sanction"
	StoreKey   = ModuleName

	GovModuleName = "gov"
)

var (
	ParamsKey                 = collections.NewPrefix("p_sanction")
	AgentKeyPrefix            = collections.NewPrefix("agent/")
	AgentBySignerKeyPrefix    = collections.NewPrefix("agent_by_signer/")
	RiskReportKeyPrefix       = collections.NewPrefix("risk_report/")
	SanctionCaseKeyPrefix     = collections.NewPrefix("case/")
	SanctionVoteKeyPrefix     = collections.NewPrefix("vote/")
	ActiveTxSanctionKeyPrefix = collections.NewPrefix("active_tx/")
	FrozenAddressKeyPrefix    = collections.NewPrefix("frozen_address/")
	ExecutionRecordKeyPrefix  = collections.NewPrefix("execution/")
)
