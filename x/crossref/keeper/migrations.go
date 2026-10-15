package keeper

import "context"

// Migrate1To2 initializes the v2 crossref store layout.
//
// v2 adds CrossReferenceAck records. The collection is empty for existing
// chains, so no state rewrite is required; the migration exists to make the
// module version transition explicit for upgrade handlers.
func (k Keeper) Migrate1To2(context.Context) error {
	return nil
}

// Migrate2To3 initializes the v3 light-client trust store layout.
//
// v3 adds TrustedLightClientState and LightClientUpdate records. Existing
// chains start with an empty trust store; domain admins should register or
// update validator_set_hash trust roots before enabling CometBFT proof mode.
func (k Keeper) Migrate2To3(context.Context) error {
	return nil
}

// v4 adds empty history-v2 collections; governance must explicitly configure a session.
func (k Keeper) Migrate3To4(context.Context) error { return nil }
