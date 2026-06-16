package keeper

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/cosmos/gogoproto/proto"
	"github.com/crossref/crossrefd/x/sanction/types"
)

func ComputePolicyAction(reports []types.RiskReport, params types.Params, targetType types.SanctionTargetType) types.SanctionAction {
	var max uint32
	for _, report := range reports {
		if report.RiskScore > max {
			max = report.RiskScore
		}
	}
	if max >= params.RevertThreshold && targetType == types.SanctionTargetType_SANCTION_TARGET_TYPE_TX {
		return types.SanctionAction_SANCTION_ACTION_REVERT_TRANSFER
	}
	if max >= params.FreezeThreshold && targetType == types.SanctionTargetType_SANCTION_TARGET_TYPE_ADDRESS {
		return types.SanctionAction_SANCTION_ACTION_FREEZE_ADDRESS
	}
	if max >= params.BlockThreshold && targetType == types.SanctionTargetType_SANCTION_TARGET_TYPE_TX {
		return types.SanctionAction_SANCTION_ACTION_BLOCK_TX
	}
	if max >= params.WatchThreshold {
		return types.SanctionAction_SANCTION_ACTION_WATCH
	}
	return types.SanctionAction_SANCTION_ACTION_UNSPECIFIED
}
func DecisionHash(sanctionCase types.SanctionCase) []byte {
	copyCase := sanctionCase
	copyCase.DecisionHash = nil
	bz, err := proto.Marshal(&copyCase)
	if err != nil {
		panic(err)
	}
	sum := sha256.Sum256(bz)
	return sum[:]
}
func txKey(txHash []byte) string            { return hex.EncodeToString(txHash) }
func voteKey(caseID, agentID string) string { return caseID + "/" + agentID }
func actionAllowed(action types.SanctionAction, params types.Params) bool {
	for _, allowed := range params.AllowedActions {
		if allowed == action {
			return true
		}
	}
	return false
}
