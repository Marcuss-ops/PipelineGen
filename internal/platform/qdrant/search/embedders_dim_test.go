// Package search — embedders_dim_test.go covers the canonical
// SigLIP dimension guard on imageEmbedderAdapter. Pure
// unit test on the validateVisualEmbeddingDim helper; no HTTP
// mocking required. Per godlike/06 SSOT, the guard is the single
// canonical validator for visual embedding dimensionality.
package search

import (
	"errors"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/models"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/qdrant/schema"
)

func TestValidateVisualEmbeddingDim(t *testing.T) {
	cases := []struct {
		name    string
		vec     []float32
		wantErr bool
	}{
		{
			name:    "happy path canonical dim",
			vec:     make([]float32, schema.VisualEmbeddingDim),
			wantErr: false,
		},
		{
			name:    "nil vector",
			vec:     nil,
			wantErr: true,
		},
		{
			name:    "empty vector",
			vec:     []float32{},
			wantErr: true,
		},
		{
			name:    "767d one short",
			vec:     make([]float32, schema.VisualEmbeddingDim-1),
			wantErr: true,
		},
		{
			name:    "769d one long",
			vec:     make([]float32, schema.VisualEmbeddingDim+1),
			wantErr: true,
		},
		{
			name:    "single dim",
			vec:     make([]float32, 1),
			wantErr: true,
		},
		{
			name:    "wildly off (512 like CLAP)",
			vec:     make([]float32, 512),
			wantErr: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := validateVisualEmbeddingDim(tc.vec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected dim-mismatch error for len=%d, got nil", len(tc.vec))
				}
				if !errors.Is(err, ErrInvalidVisualEmbeddingDim) {
					t.Fatalf("expected ErrInvalidVisualEmbeddingDim via errors.Is, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error for len=%d, got %v", len(tc.vec), err)
			}
		})
	}
}

func TestErrInvalidVisualEmbeddingDim_SentinelIsReachable(t *testing.T) {
	// Confirm the sentinel is reachable via errors.Is wrap chains
	// (important for callers that wrap with %w and want to do typed
	// error matching at the API boundary).
	if !errors.Is(ErrInvalidVisualEmbeddingDim, ErrInvalidVisualEmbeddingDim) {
		t.Fatal("sentinel should be reachable via errors.Is (self-comparison)")
	}
	// Guard a wrong-dim vector produces a wrapped error that
	// errors.Is can still unwrap to the sentinel.
	wrapped := validateVisualEmbeddingDim(make([]float32, 100))
	if wrapped == nil {
		t.Fatal("expected wrapped sentinel for len=100")
	}
	if !errors.Is(wrapped, ErrInvalidVisualEmbeddingDim) {
		t.Fatal("validateVisualEmbeddingDim should wrap ErrInvalidVisualEmbeddingDim via errors.Is")
	}
}

func TestSchemaVisualEmbeddingConstants_Stable(t *testing.T) {
	// Lock the canonical 768d + "2026-06-16-v1" pair. Changing any
	// of these requires a Qdrant schema migration (the named
	// vector "visual" carries these dims, the
	// media_assets.metadata_json.embedding_version_visual field
	// pins against this string). Operators + downstream tooling
	// rely on these being stable (godlike/06 SSOT).
	// Lock the canonical SigLIP so400m-patch14-384 native output (1152d,
	// probed from the production sidecar) + "2026-06-16-v1" pair. Changing
	// either requires a Qdrant schema migration (godlike/06 SSOT).
	if schema.VisualEmbeddingDim != models.CanonicalVisualModelDimensions {
		t.Fatalf("VisualEmbeddingDim drifted from %d to %d — needs a Qdrant schema migration",
			models.CanonicalVisualModelDimensions, schema.VisualEmbeddingDim)
	}
	if schema.VisualEmbeddingModelVersion != "2026-06-16-v1" {
		t.Fatalf("VisualEmbeddingModelVersion drifted from 2026-06-16-v1 to %q — needs a Qdrant schema migration",
			schema.VisualEmbeddingModelVersion)
	}
}
