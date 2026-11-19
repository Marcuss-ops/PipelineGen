package cliprender

// materializer_nil_test.go — ticket CR-19-10: AssetMaterializer promises a
// verified artifact on success. An adapter that returns (nil, nil) violates the
// port; the preparer must turn that into a typed error before any dereference
// (the source gate would otherwise close with a nil error and the transcript
// generator would read source.SHA256).

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
)

type nilReturningMaterializer struct{}

func (nilReturningMaterializer) Materialize(context.Context, AssetRef) (*MaterializedAsset, error) {
	return nil, nil
}

func newNilMaterializerPreparer(t *testing.T) *Preparer {
	t.Helper()
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	p, err := NewPreparer(resolver, nilReturningMaterializer{}, &fakeTranscriptResolver{}, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatalf("NewPreparer: %v", err)
	}
	return p
}

func TestPrepare_MaterializerNilResultIsTypedError(t *testing.T) {
	// A READY reused transcript keeps generation out of the wave, so the only
	// failing phase is the source materialization itself.
	req := baseRenderRequest()
	req.Transcript.Mode = TranscriptModeReuse
	transcript := &fakeTranscriptResolver{
		existing:   &TranscriptResult{Text: "ready transcript", Cues: []Cue{{StartMs: 0, EndMs: 100, Text: "hi"}}},
		existingOK: true,
	}
	resolver := newFakeAssetResolver(map[string]AssetRef{
		"asset-source": {AssetID: "asset-source"},
	})
	p, err := NewPreparer(resolver, nilReturningMaterializer{}, transcript, NewContractResolver(), zap.NewNop())
	if err != nil {
		t.Fatalf("NewPreparer: %v", err)
	}

	_, err = p.Prepare(context.Background(), req, "run-nil-materializer")
	if !errors.Is(err, ErrMaterializerReturnedNoArtifact) {
		t.Fatalf("Prepare error = %v, want ErrMaterializerReturnedNoArtifact", err)
	}
}

func TestMaterializeAsset_NilResultIsTypedError(t *testing.T) {
	p := newNilMaterializerPreparer(t)
	mat, err := p.MaterializeAsset(context.Background(), "asset-source")
	if mat != nil {
		t.Fatalf("materialize returned %+v, want nil", mat)
	}
	if !errors.Is(err, ErrMaterializerReturnedNoArtifact) {
		t.Fatalf("MaterializeAsset error = %v, want ErrMaterializerReturnedNoArtifact", err)
	}
}
