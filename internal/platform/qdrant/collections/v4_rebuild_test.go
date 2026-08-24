package collections

import (
	"context"
	"errors"
	"testing"

	capregistry "github.com/Marcuss-ops/PipelineGen/internal/capabilities/mediaregistry"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
	platformschema "github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

// fakeProjectionManager records the lifecycle calls RebuildV4Projection
// drives, so the test can pin ordering + the derived collection name.
type fakeProjectionManager struct {
	buildCollection string
	validateCalls   []string
	activateCalls   []string
	populateCalled  bool
	buildErr        error
	validateErr     error
	activateErr     error
}

func (f *fakeProjectionManager) BuildProjectionWith(_ context.Context, _ string, collection string, _ int64, populate ProjectionPopulateFunc) error {
	f.buildCollection = collection
	if f.buildErr != nil {
		return f.buildErr
	}
	if populate != nil {
		if err := populate(context.Background(), collection); err != nil {
			return err
		}
		f.populateCalled = true
	}
	return nil
}

func (f *fakeProjectionManager) ValidateProjection(_ context.Context, projectionID string, _ int64, _ int) (*schema.SwitchReport, error) {
	f.validateCalls = append(f.validateCalls, projectionID)
	if f.validateErr != nil {
		return nil, f.validateErr
	}
	return &schema.SwitchReport{Ready: true}, nil
}

func (f *fakeProjectionManager) ActivateProjection(_ context.Context, projectionID string, _ int64) error {
	f.activateCalls = append(f.activateCalls, projectionID)
	return f.activateErr
}

// The remaining ProjectionManager methods are stubs — the orchestrator only
// exercises BuildProjectionWith / ValidateProjection / ActivateProjection.
func (f *fakeProjectionManager) Build(context.Context, string, string, int64) error { return nil }
func (f *fakeProjectionManager) BuildProjection(context.Context, string, string, int64) error {
	return nil
}
func (f *fakeProjectionManager) Validate(context.Context, string, int64, int) (*schema.SwitchReport, error) {
	return &schema.SwitchReport{Ready: true}, nil
}
func (f *fakeProjectionManager) Activate(context.Context, string, int64) error { return nil }
func (f *fakeProjectionManager) Rollback(context.Context, string, string) error {
	return nil
}
func (f *fakeProjectionManager) RollbackProjection(context.Context, string, string) error {
	return nil
}
func (f *fakeProjectionManager) Rebuild(context.Context, string, string, int64, ProjectionPopulateFunc) error {
	return nil
}
func (f *fakeProjectionManager) RebuildProjection(context.Context, string, string, int64, ProjectionPopulateFunc) error {
	return nil
}
func (f *fakeProjectionManager) GetStatus(string) (capregistry.ProjectionStatus, error) {
	return capregistry.ProjectionActive, nil
}
func (f *fakeProjectionManager) Projection(string) (capregistry.Projection, bool) {
	return capregistry.Projection{}, false
}

var _ ProjectionManager = (*fakeProjectionManager)(nil)

func deterministicGolden() GoldenQueryExecutor {
	return func(_ context.Context, _ string, _ string, topK int) ([]string, error) {
		ids := make([]string, topK)
		for i := range ids {
			ids[i] = string(rune('A' + i))
		}
		return ids, nil
	}
}

func TestRebuildV4Projection_DerivesSignedNameAndRunsLifecycle(t *testing.T) {
	pm := &fakeProjectionManager{}
	sig := platformschema.CanonicalV4Signature()
	wantName, err := sig.PhysicalName()
	if err != nil {
		t.Fatalf("PhysicalName: %v", err)
	}

	result, err := RebuildV4Projection(context.Background(), pm, sig, "build-v4", 42, 10, nil, deterministicGolden())
	if err != nil {
		t.Fatalf("RebuildV4Projection: %v", err)
	}
	if result.CollectionName != wantName {
		t.Fatalf("CollectionName = %q, want %q", result.CollectionName, wantName)
	}
	if pm.buildCollection != wantName {
		t.Fatalf("BuildProjectionWith collection = %q, want signed name %q", pm.buildCollection, wantName)
	}
	if len(pm.validateCalls) != 1 || pm.validateCalls[0] != "build-v4" {
		t.Fatalf("validate calls = %v, want [build-v4]", pm.validateCalls)
	}
	if len(pm.activateCalls) != 1 || pm.activateCalls[0] != "build-v4" {
		t.Fatalf("activate calls = %v, want [build-v4]", pm.activateCalls)
	}
	if !result.GoldenQueriesCertified || !result.Activated {
		t.Fatalf("result = %+v, want certified + activated", result)
	}
}

func TestRebuildV4Projection_InvalidSignatureFailsBeforeBuild(t *testing.T) {
	pm := &fakeProjectionManager{}
	sig := platformschema.CanonicalV4Signature()
	sig.EmbeddingContractHash = "not-a-hash"

	if _, err := RebuildV4Projection(context.Background(), pm, sig, "build-v4", 42, 10, nil, deterministicGolden()); err == nil {
		t.Fatal("RebuildV4Projection must fail on an invalid signature")
	}
	if pm.buildCollection != "" {
		t.Fatalf("Build must not run for an invalid signature, got collection %q", pm.buildCollection)
	}
}

func TestRebuildV4Projection_GoldenDriftFails(t *testing.T) {
	pm := &fakeProjectionManager{}
	// The golden executor returns drifting top-10 on the second run.
	run := 0
	golden := func(_ context.Context, _ string, _ string, topK int) ([]string, error) {
		ids := make([]string, topK)
		for i := range ids {
			if run > 0 {
				ids[i] = string(rune('J' - i)) // reverse
			} else {
				ids[i] = string(rune('A' + i))
			}
		}
		run++
		return ids, nil
	}

	if _, err := RebuildV4Projection(context.Background(), pm, platformschema.CanonicalV4Signature(), "build-v4", 42, 10, nil, golden); err == nil {
		t.Fatal("RebuildV4Projection must fail on golden query drift")
	}
	if len(pm.activateCalls) != 0 {
		t.Fatal("Activate must not run after golden drift")
	}
}

func TestRebuildV4Projection_ValidateFailureStopsLifecycle(t *testing.T) {
	pm := &fakeProjectionManager{validateErr: errors.New("point count mismatch")}
	if _, err := RebuildV4Projection(context.Background(), pm, platformschema.CanonicalV4Signature(), "build-v4", 42, 10, nil, deterministicGolden()); err == nil {
		t.Fatal("RebuildV4Projection must propagate a validation failure")
	}
	if len(pm.activateCalls) != 0 {
		t.Fatal("Activate must not run after a validation failure")
	}
}
