package keeper

import (
	"context"
	"cosmossdk.io/errors"
	sdkmath "cosmossdk.io/math"
	"crypto/sha256"
	"fmt"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/crossref/crossrefd/x/sanction/types"
)

func (k Keeper) IsAddressFrozen(ctx context.Context, address string) bool {
	found, err := k.FrozenAddresses.Has(ctx, address)
	return err == nil && found
}
func (k Keeper) SendCoinsAllowed(ctx context.Context, fromAddr, toAddr string) error {
	if k.IsAddressFrozen(ctx, fromAddr) {
		return errors.Wrapf(types.ErrAddressAlreadyFrozen, "from=%s", fromAddr)
	}
	if k.IsAddressFrozen(ctx, toAddr) {
		return errors.Wrapf(types.ErrAddressAlreadyFrozen, "to=%s", toAddr)
	}
	return nil
}
func (k Keeper) executeEscrow(ctx context.Context, sanctionCase types.SanctionCase) error {
	if k.bankKeeper == nil {
		return nil
	}
	report, ok := k.primaryReport(ctx, sanctionCase)
	if !ok {
		return nil
	}
	coins, err := reportCoins(report)
	if err != nil {
		return err
	}
	from, err := sdk.AccAddressFromBech32(report.FromAddress)
	if err != nil {
		return err
	}
	return k.bankKeeper.SendCoinsFromAccountToModule(ctx, from, types.ModuleName, coins)
}
func (k Keeper) executeRevert(ctx context.Context, sanctionCase types.SanctionCase) error {
	if k.bankKeeper == nil {
		return nil
	}
	report, ok := k.primaryReport(ctx, sanctionCase)
	if !ok {
		return nil
	}
	coins, err := reportCoins(report)
	if err != nil {
		return err
	}
	from, err := sdk.AccAddressFromBech32(report.ToAddress)
	if err != nil {
		return err
	}
	to, err := sdk.AccAddressFromBech32(report.FromAddress)
	if err != nil {
		return err
	}
	return k.bankKeeper.SendCoins(ctx, from, to, coins)
}
func (k Keeper) primaryReport(ctx context.Context, sanctionCase types.SanctionCase) (types.RiskReport, bool) {
	if len(sanctionCase.ReportIds) == 0 {
		return types.RiskReport{}, false
	}
	report, err := k.RiskReports.Get(ctx, sanctionCase.ReportIds[0])
	return report, err == nil
}
func reportCoins(report types.RiskReport) (sdk.Coins, error) {
	amount, ok := sdkmath.NewIntFromString(report.Amount)
	if !ok {
		return nil, fmt.Errorf("invalid report amount: %s", report.Amount)
	}
	return sdk.NewCoins(sdk.NewCoin(report.Denom, amount)), nil
}
func stateChangeHash(record types.ExecutionRecord) []byte {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%x/%s/%d", record.CaseId, types.ActionName(record.Action), record.TxHash, record.TargetAddress, record.ExecutedHeight)))
	return sum[:]
}
