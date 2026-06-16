package keeper

import (
	abci "github.com/cometbft/cometbft/abci/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
)

func (k Keeper) PrepareProposalHandler() sdk.PrepareProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestPrepareProposal) (*abci.ResponsePrepareProposal, error) {
		txs, err := k.FilterSanctionedTxs(ctx, req.Txs)
		if err != nil {
			return nil, err
		}
		return &abci.ResponsePrepareProposal{Txs: txs}, nil
	}
}
func (k Keeper) ProcessProposalHandler() sdk.ProcessProposalHandler {
	return func(ctx sdk.Context, req *abci.RequestProcessProposal) (*abci.ResponseProcessProposal, error) {
		has, err := k.ContainsSanctionedTx(ctx, req.Txs)
		if err != nil {
			return nil, err
		}
		if has {
			return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_REJECT}, nil
		}
		return &abci.ResponseProcessProposal{Status: abci.ResponseProcessProposal_ACCEPT}, nil
	}
}
func (k Keeper) CheckTxAllowed(ctx sdk.Context, tx []byte) error {
	found, err := k.ActiveTxSanctions.Has(ctx, txKey(TxHash(tx)))
	if err != nil {
		return err
	}
	if found {
		return sdkerrors.ErrUnauthorized.Wrap("transaction is sanctioned")
	}
	return nil
}
