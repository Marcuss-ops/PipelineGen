package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/assetop"
)

func TestProcessAsset_FinalizerMissingFailsClosed(t *testing.T) {
	svc := NewService(ServiceDeps{}, Config{
		PersistPolicy: assetop.PersistPolicy{SaveToAssetRegistry: true},
	})

	result, err := svc.ProcessAsset(context.Background(), &FinalizeInput{}, "hash")
	if result != nil {
		t.Fatalf("result = %#v, want nil when finalizer is unavailable", result)
	}
	if !errors.Is(err, ErrFinalizerUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrFinalizerUnavailable)", err)
	}
}

func TestReconcile_ReconcilerMissingFailsClosed(t *testing.T) {
	svc := NewService(ServiceDeps{}, Config{
		ReconcilePolicy: assetop.ReconcilePolicy{Enabled: true},
	})

	count, err := svc.Reconcile(context.Background(), "images")
	if count != 0 {
		t.Fatalf("count = %d, want 0 when reconciler is unavailable", count)
	}
	if !errors.Is(err, ErrReconcilerUnavailable) {
		t.Fatalf("err = %v, want errors.Is(ErrReconcilerUnavailable)", err)
	}
}
