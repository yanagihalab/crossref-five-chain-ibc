package types

const (
	DefaultCheckpointProofMaxLag = uint64(10000)
	DefaultProofSpamWindowBlocks = uint64(1000)
	DefaultProofSpamMaxFailures  = uint64(10)
)

// NewParams creates a new Params instance.
func NewParams() Params {
	return Params{
		RequireStrictHysteresisSignature: true,
		CheckpointProofMaxLag:            DefaultCheckpointProofMaxLag,
		RequireConsensusProof:            false,
		ProofSpamWindowBlocks:            DefaultProofSpamWindowBlocks,
		ProofSpamMaxFailures:             DefaultProofSpamMaxFailures,
	}
}

// DefaultParams returns a default set of parameters.
func DefaultParams() Params {
	return NewParams()
}

// Validate validates the set of params.
func (p Params) Validate() error {
	// Zero values are accepted for genesis and update compatibility. Runtime
	// readers treat zero as the module default.
	return nil
}
