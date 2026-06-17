package types

// DONTCOVER

import (
	"cosmossdk.io/errors"
)

// x/crossref module sentinel errors
var (
	ErrInvalidSigner               = errors.Register(ModuleName, 1100, "expected gov account as only signer for proposal message")
	ErrInvalidPacketTimeout        = errors.Register(ModuleName, 1500, "invalid packet timeout")
	ErrInvalidVersion              = errors.Register(ModuleName, 1501, "invalid version")
	ErrInvalidRequest              = errors.Register(ModuleName, 1502, "invalid request")
	ErrDomainNotFound              = errors.Register(ModuleName, 1503, "domain not found")
	ErrDomainChannelNotFound       = errors.Register(ModuleName, 1504, "domain channel not found")
	ErrUnauthorizedChannel         = errors.Register(ModuleName, 1505, "channel is not authorized for domain")
	ErrCheckpointNotFound          = errors.Register(ModuleName, 1506, "checkpoint not found")
	ErrCheckpointConflict          = errors.Register(ModuleName, 1507, "checkpoint conflict")
	ErrCheckpointHashMismatch      = errors.Register(ModuleName, 1508, "checkpoint hash mismatch")
	ErrPreviousCheckpointBroken    = errors.Register(ModuleName, 1509, "previous checkpoint hash does not match latest known checkpoint")
	ErrSourceChainMismatch         = errors.Register(ModuleName, 1510, "packet source chain does not match registered domain")
	ErrCheckpointProofRequired     = errors.Register(ModuleName, 1511, "checkpoint membership proof is required")
	ErrCheckpointProofInvalid      = errors.Register(ModuleName, 1512, "checkpoint membership proof is invalid")
	ErrHysteresisSignatureRequired = errors.Register(ModuleName, 1513, "hysteresis signature is required")
	ErrHysteresisSignatureInvalid  = errors.Register(ModuleName, 1514, "hysteresis signature is invalid")
	ErrReplayPacket                = errors.Register(ModuleName, 1515, "cross-reference packet replay detected")
	ErrCheckpointProofStale        = errors.Register(ModuleName, 1516, "checkpoint membership proof is stale")
)
