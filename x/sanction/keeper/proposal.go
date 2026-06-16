package keeper

import (
	"context"
	"github.com/cometbft/cometbft/crypto/tmhash"
)

func TxHash(tx []byte) []byte { return tmhash.Sum(tx) }
func (k Keeper) FilterSanctionedTxs(ctx context.Context, txs [][]byte) ([][]byte, error) {
	out := make([][]byte, 0, len(txs))
	for _, tx := range txs {
		found, err := k.ActiveTxSanctions.Has(ctx, txKey(TxHash(tx)))
		if err != nil {
			return nil, err
		}
		if !found {
			out = append(out, tx)
		}
	}
	return out, nil
}
func (k Keeper) ContainsSanctionedTx(ctx context.Context, txs [][]byte) (bool, error) {
	for _, tx := range txs {
		found, err := k.ActiveTxSanctions.Has(ctx, txKey(TxHash(tx)))
		if err != nil {
			return false, err
		}
		if found {
			return true, nil
		}
	}
	return false, nil
}
