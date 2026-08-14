package embedding

import (
	"errors"
	"fmt"
)

// ErrContractMismatch is the sentinel error every contract mismatch wraps.
// Use errors.Is(err, ErrContractMismatch) to detect it.
var ErrContractMismatch = errors.New("embedding contract mismatch")

// MismatchError reports which component diverged from the canonical contract.
// Its code is the operator-facing gate identifier
// QDRANT_EMBEDDING_CONTRACT_MISMATCH.
type MismatchError struct {
	// Component is the leg of the handshake that diverged (see the
	// Component* constants in handshake.go).
	Component string
	// Expected is the canonical contract.
	Expected Contract
	// Got is the observed contract that diverged.
	Got Contract
}

// Error implements error.
func (e *MismatchError) Error() string {
	return fmt.Sprintf("QDRANT_EMBEDDING_CONTRACT_MISMATCH: %s diverged: expected %s, got %s",
		e.Component, e.Expected.String(), e.Got.String())
}

// Unwrap lets errors.Is(err, ErrContractMismatch) succeed.
func (e *MismatchError) Unwrap() error { return ErrContractMismatch }

// Code returns the canonical fail-closed gate identifier.
func (e *MismatchError) Code() string { return "QDRANT_EMBEDDING_CONTRACT_MISMATCH" }
