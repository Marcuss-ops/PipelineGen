// Package commit (asset/commit) owns the asset-commit surface:
// takes a published artifact + caller-owned transaction, returns
// the canonical commit (asset row + versions + locations +
// renditions + outbox events) inside the same transaction.
//
// PR-YOUTUBE-SERVICE-SPLIT (July 2026, phase 1): typed-narrow
// godlike/06 SSOT contract is in place. The TxAdapter
// constructor accepts the canonical *finalizer.AssetTxFinalizer
// so the composition root can validate wiring at boot
// (godlike/07 fail-closed); the actual Commit /
// FirePostCommitHooks delegation is DEFERRED to phase 2 until
// the finalization.RenditionOutput field type + artifact
// projection are fully verified.
package commit

import (
	"context"
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/finalizer"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// Committer is the canonical narrow port.
type Committer interface {
	Commit(ctx context.Context, tx Transaction, artifact Artifact) (Ref, []OutboxEvent, error)
	FirePostCommitHooks(ctx context.Context, artifact Artifact)
}

// Artifact mirrors the canonical finalization.PublishedArtifact
// fields needed by the commit surface.
//
// Renditions is []finalization.RenditionOutput (the canonical
// renamed type — was mis-typed as ArtifactRendition in phase 1's
// first draft; reconciled here).
type Artifact struct {
	ArtifactID       string
	Kind             finalization.ArtifactKind
	Filename         string
	Description      string
	SizeBytes        int64
	MIMEType         string
	SHA256           string
	Source           string
	SourceVersion    int64
	SourceTextHash   string
	SourceLanguage   string
	ArtifactMetadata map[string]any
	Location         finalization.AssetLocation
	Renditions       []finalization.AssetRenditionLocation
}

// Ref mirrors the canonical finalization.ArtifactRef surface.
type Ref struct {
	ArtifactID    string
	AssetID       string
	Kind          finalization.ArtifactKind
	SourceVersion int64
	ContentHash   string
}

// OutboxEvent mirrors the canonical finalization.OutboxEvent.
type OutboxEvent struct {
	EventType   string
	AggregateID string
	EventKey    string
	Payload     []byte
}

// Transaction is the typed-narrow package-local alias for
// finalization.Transaction.
type Transaction = finalization.Transaction

// TxAdapter is the canonical Committer impl.
type TxAdapter struct {
	finalizer *finalizer.AssetTxFinalizer
}

// NewTxAdapter constructs the canonical Committer. nil
// finalizer → ErrCommitterNotWired (godlike/07 fail-closed).
func NewTxAdapter(f *finalizer.AssetTxFinalizer) (*TxAdapter, error) {
	if f == nil {
		return nil, ErrCommitterNotWired
	}
	return &TxAdapter{finalizer: f}, nil
}

// ErrCommitterNotWired is the construction-time typed sentinel.
var ErrCommitterNotWired = fmt.Errorf("asset/commit: committer not wired (godlike/07 fail-closed)")

// ErrCommitterNotImplemented is the phase-1 typed sentinel.
// godlike/07 NO-FAKE-AVAILABILITY: never silent empty result.
var ErrCommitterNotImplemented = fmt.Errorf("asset/commit: canonical Commit/FirePostCommitHooks delegation deferred to phase 2 (godlike/07 typed sentinel; finalization.RenditionOutput projection pending)")

// Commit returns the phase-1 typed sentinel. Phase 2 will
// delegate to finalizer.FinalizeAsset / FirePostCommitHooks.
//
// godlike/06 SSOT caller-owned-tx discipline preserved: the
// caller still owns BeginTx / Commit / Rollback — the typed
// sentinel is the user-facing failure signal.
func (c *TxAdapter) Commit(ctx context.Context, tx Transaction, art Artifact) (Ref, []OutboxEvent, error) {
	if c == nil {
		return Ref{}, nil, ErrCommitterNotWired
	}
	if art.ArtifactID == "" {
		return Ref{}, nil, fmt.Errorf("asset/commit: ArtifactID is required (godlike/07 fail-closed)")
	}
	// Deferral sentinel — typed-branchable via errors.Is.
	return Ref{}, nil, fmt.Errorf("%w (artifact_id=%q)", ErrCommitterNotImplemented, art.ArtifactID)
}

// FirePostCommitHooks is a no-op in phase 1.
func (c *TxAdapter) FirePostCommitHooks(ctx context.Context, art Artifact) {
	if c == nil {
		return
	}
	// Phase-2 will call c.finalizer.FirePostCommitHooks(ctx, pubArtifact).
	// Phase-1: silent no-op (the caller is responsible for checking
	// commit results; this hook is a fanout, not a fail-closed step).
}

// Compile-time pinning: *TxAdapter satisfies Committer.
var _ Committer = (*TxAdapter)(nil)
