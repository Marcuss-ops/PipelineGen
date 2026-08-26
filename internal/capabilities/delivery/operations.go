package delivery

import (
)

import "context"

// Operation identifies the two distinct asset-delivery side effects.
// The empty value is reserved for the legacy delivery.requested envelope.
type Operation string

const (
	OperationRegisterRemoteReference Operation = "register_remote_reference"
	OperationMaterializeLocal        Operation = "materialize_local"
)

// RemoteReferenceRequest contains metadata needed to register an asset that
// remains remote. Registration must not claim that local bytes exist.
type RemoteReferenceRequest struct {
	AssetID        string
	Provider       string
	RemoteURL      string
	DestinationID  string
	AccountID      string
	SHA256         string
	SizeBytes      int64
	MIMEType       string
	IdempotencyKey string
}

// RemoteReferenceRegistrar persists or publishes a remote asset reference.
// Implementations own provider-specific persistence and credentials.
type RemoteReferenceRegistrar interface {
	RegisterRemoteReference(ctx context.Context, req RemoteReferenceRequest) error
}

// MaterializationRequest contains the source and integrity metadata required
// to acquire local bytes. Implementations own the workspace/filesystem policy.
type MaterializationRequest struct {
	AssetID        string
	Provider       string
	RemoteURL      string
	StorageKey     string
	DestinationID  string
	AccountID      string
	Filename       string
	SHA256         string
	SizeBytes      int64
	MIMEType       string
	IdempotencyKey string
}

// Materializer acquires and verifies local bytes for a previously referenced
// asset. A successful call is the only point at which local availability may
// be recorded by the implementation.
type Materializer interface {
	MaterializeLocal(ctx context.Context, req MaterializationRequest) error
}
