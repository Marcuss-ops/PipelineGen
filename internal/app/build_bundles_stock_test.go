// Package app — build_bundles_stock_test.go: TDD coverage for the
// godlike/07 symmetric production gate at the stock pipeline
// composition root.
//
// PR-STOCK-ATLASTORCH-DISPATCH commit-2 (godlike/07 fail-fast-at-composition).
// The 4-state gate at validateStockSymmetricGate mirrors the late-binding
// gate at orchestrator.go:478/480 but surfaces asymmetric wiring at
// startup rather than at first /run. This test file pins the
// contract so a future regression that drops the gate (or swaps the
// mismatched-error direction) fails loud at go-test time.
package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/finalization"
)

// noOpPublisher is a structurally conformant delivery.Publisher for
// the gate test — we don't need a real Drive write canal; we just
// need a non-nil value to flip the gate into the asymmetric branch.
// Per the canonical interface at
// internal/application/assets/delivery/publisher.go:40, Publisher has
// TWO methods: Publish (returns *PublishResult) + ResolveFolder
// (returns folder ID). BOTH must be present to satisfy the
// interface at compile time.
type noOpPublisher struct{}

// Publish returns a *PublishResult (canonical interface signature
// per delivery/publisher.go:46).
func (noOpPublisher) Publish(_ context.Context, _ delivery.PublishRequest) (*delivery.PublishResult, error) {
	return nil, errors.New("noOpPublisher: not for production use")
}

// ResolveFolder returns a folder ID (canonical interface signature
// per delivery/publisher.go:51).
func (noOpPublisher) ResolveFolder(_ context.Context, _ delivery.PublishRequest) (string, error) {
	return "", errors.New("noOpPublisher.ResolveFolder: not for production use")
}

// Compile-time anchor: noOpPublisher is a delivery.Publisher. If
// the interface signature drifts, go test fails to compile. Pinned
// here so the file's own conformance is asserted (not just inferred
// via the validateStockSymmetricGate call below). Mirrors the
// `var _ stockpipeline.ServiceRunner = (*stockpipeline.Service)(nil)`
// canonical SSOT pattern.
var _ delivery.Publisher = (*noOpPublisher)(nil)

// noOpFinalizer is a structurally conformant finalization.JobFinalizer
// for the gate test. The canonical interface at
// internal/domain/finalization/interfaces.go:65 declares ONE method:
// CompleteWithArtifacts(ctx, FinalizationRequest) (*FinalizationResult, error).
// Note: the field types in FinalizationRequest (Lease, Result,
// Artifacts, OptionalDeclarations, Events) are STRUCTS — passing a
// zero-valued concrete satisfies the signature without manual field
// population (the gate test doesn't introspect the typed envelope,
// only the typed error).
type noOpFinalizer struct{}

// CompleteWithArtifacts returns a *FinalizationResult (canonical
// interface signature per finalization/interfaces.go:65).
func (noOpFinalizer) CompleteWithArtifacts(_ context.Context, _ finalization.FinalizationRequest) (*finalization.FinalizationResult, error) {
	return nil, errors.New("noOpFinalizer: not for production use")
}

// Compile-time anchor: noOpFinalizer is a finalization.JobFinalizer.
var _ finalization.JobFinalizer = (*noOpFinalizer)(nil)

// TestValidateStockSymmetricGate pins the 4-state gate contract.
// The 4 cases map 1:1 to the godlike/07 production-mode decision tree:
// publisher=nil + finalizer=nil → OK (test/backcompat)
// publisher≠nil + finalizer≠nil → OK (production)
// publisher≠nil + finalizer=nil → ErrStockProductionJobFinalizerMissing
// publisher=nil + finalizer≠nil → ErrStockProductionArtifactPrepMissing.
func TestValidateStockSymmetricGate(t *testing.T) {
	pub := noOpPublisher{}
	fin := noOpFinalizer{}
	cases := []struct {
		name      string
		publisher delivery.Publisher
		finalizer finalization.JobFinalizer
		wantErr   error // nil = OK, non-nil = must errors.Is
	}{
		{"both nil → test/backcompat mode OK", nil, nil, nil},
		{"both non-nil → production mode OK", pub, fin, nil},
		{"publisher non-nil + finalizer nil → ErrStockProductionJobFinalizerMissing",
			pub, nil, stockpipeline.ErrStockProductionJobFinalizerMissing},
		{"publisher nil + finalizer non-nil → ErrStockProductionArtifactPrepMissing",
			nil, fin, stockpipeline.ErrStockProductionArtifactPrepMissing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateStockSymmetricGate(tc.publisher, tc.finalizer)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("validateStockSymmetricGate: want nil, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateStockSymmetricGate: want %v, got nil", tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("validateStockSymmetricGate: want errors.Is(%v) to hold, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestValidateStockSymmetricGate_PinErrorMessages locks the canonical
// human-readable message text on each sentinel. A future refactor that
// rewrites the message strings (e.g. to include a workspace name)
// fails loud here. Per godlike/07 typed-error contract: the sentinel
// identity (errors.Is) is the load-bearing assertion; the message is
// a diagnostic surface that operators + dashboards depend on for
// incident triage.
func TestValidateStockSymmetricGate_PinErrorMessages(t *testing.T) {
	pub := noOpPublisher{}
	fin := noOpFinalizer{}

	t.Run("ErrStockProductionJobFinalizerMissing message", func(t *testing.T) {
		err := validateStockSymmetricGate(pub, nil)
		if !errors.Is(err, stockpipeline.ErrStockProductionJobFinalizerMissing) {
			t.Fatalf("want ErrStockProductionJobFinalizerMissing, got %v", err)
		}
		// canonical SSOT string from upload_orchestration.go:232
		const wantMsg = "stock: production gate — JobFinalizer nil while ArtifactPreparation wired (call WithJobFinalizer before RunResilient)"
		if got := err.Error(); got != wantMsg {
			t.Fatalf("message drift: want %q, got %q", wantMsg, got)
		}
	})

	t.Run("ErrStockProductionArtifactPrepMissing message", func(t *testing.T) {
		err := validateStockSymmetricGate(nil, fin)
		if !errors.Is(err, stockpipeline.ErrStockProductionArtifactPrepMissing) {
			t.Fatalf("want ErrStockProductionArtifactPrepMissing, got %v", err)
		}
		// canonical SSOT string from upload_orchestration.go:222 (swapped-pair direction)
		const wantMsg = "stock: production gate — ArtifactPreparation nil while JobFinalizer wired (call WithAssetPreparation before RunResilient)"
		if got := err.Error(); got != wantMsg {
			t.Fatalf("message drift: want %q, got %q", wantMsg, got)
		}
	})
}
