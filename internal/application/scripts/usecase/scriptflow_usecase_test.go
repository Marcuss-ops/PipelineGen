package usecase

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// ── SemaphoreUseCase ────────────────────────────────────────────────────────

func TestSemaphoreUseCase_NewRejectsZeroCapacity(t *testing.T) {
	_, err := NewSemaphoreUseCase(0, zap.NewNop())
	require.ErrorIs(t, err, ErrSemaphoreMisconfigured)
	_, err = NewSemaphoreUseCase(-1, zap.NewNop())
	require.ErrorIs(t, err, ErrSemaphoreMisconfigured)
}

func TestSemaphoreUseCase_AcquireReturnRelease(t *testing.T) {
	t.Parallel()
	uc, err := NewSemaphoreUseCase(2, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, 2, uc.Capacity())

	r1, err := uc.Acquire(context.Background(), "jobA")
	require.NoError(t, err)
	r2, err := uc.Acquire(context.Background(), "jobB")
	require.NoError(t, err)
	require.Equal(t, int64(2), uc.AcquireCount())
	require.Equal(t, int64(0), uc.ReleaseCount())

	r1()
	r2()
	require.Equal(t, int64(2), uc.ReleaseCount())
}

func TestSemaphoreUseCase_DoubleReleaseIsNoop(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	r, err := uc.Acquire(context.Background(), "jobA")
	require.NoError(t, err)
	r()
	r() // second call must not panic or decrement counter
	require.Equal(t, int64(1), uc.ReleaseCount())
}

func TestSemaphoreUseCase_AcquireCanceledByCtx(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	_, err := uc.Acquire(context.Background(), "first") // occupies slot
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = uc.Acquire(ctx, "second")
	require.ErrorIs(t, err, ErrSemaphoreAcquisitionCanceled)
}

func TestSemaphoreUseCase_AcquireReleaseReuse(t *testing.T) {
	t.Parallel()
	uc, _ := NewSemaphoreUseCase(1, zap.NewNop())
	for i := 0; i < 5; i++ {
		r, err := uc.Acquire(context.Background(), "j")
		require.NoError(t, err)
		require.NotNil(t, r)
		r()
	}
	require.Equal(t, int64(5), uc.AcquireCount())
	require.Equal(t, int64(5), uc.ReleaseCount())
}

// TestSemaphoreUseCase_NilSafe is now in semaphore_usecase_test.go

// (Sprint 1.0: DocumentsUseCase was retired — document generation
// now lives at internal/application/document/usecase.go as the
// document.generate downstream job.)

// ── PostGenUseCase (Wave 14 problem #4 fixup) ──────────────────────────────

// fakePostGenExtractor is a stand-in EntityScriptExtractor for the
// PostGenUseCase.Run test table. It captures call count + lets tests
// inject a canned FullEntityAnalysis or an error.
type fakePostGenExtractor struct {
	calls     atomic.Int32
	analysis  *asset.FullEntityAnalysis
	returnErr error
}

func (f *fakePostGenExtractor) ExtractEntitiesFromScriptWithModel(_ context.Context, _ []string, _ int, _ string) (*asset.FullEntityAnalysis, error) {
	f.calls.Add(1)
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	if f.analysis != nil {
		return f.analysis, nil
	}
	return &asset.FullEntityAnalysis{}, nil
}

// fakePostGenInsight is the InsightBuilder narrow-port fake.
type fakePostGenInsight struct {
	calls atomic.Int32
	out   ScriptInsights
}

func (f *fakePostGenInsight) Build(_ context.Context, _, _, _ string) ScriptInsights {
	f.calls.Add(1)
	return f.out
}

func TestPostGenUseCase_NilSafe(t *testing.T) {
	t.Parallel()
	var uc *PostGenUseCase
	res, err := uc.Run(context.Background(), &scriptpkg.GenerationSpec{ExtractEntities: true}, "sample script")
	require.NoError(t, err)
	assert.Empty(t, res.EntitiesJSON)
	assert.Nil(t, res.VideoMetadata)
}

func TestPostGenUseCase_EmptyScriptBypasses(t *testing.T) {
	t.Parallel()
	extractor := &fakePostGenExtractor{}
	uc := NewPostGenUseCase(extractor, nil, nil, "llama3", zap.NewNop())
	res, err := uc.Run(context.Background(), &scriptpkg.GenerationSpec{ExtractEntities: true, GenerateMetadata: true}, "")
	require.NoError(t, err)
	assert.Empty(t, res.EntitiesJSON)
	assert.Nil(t, res.VideoMetadata)
	assert.Equal(t, int32(0), extractor.calls.Load(), "empty script must not invoke the extractor")
}

func TestPostGenUseCase_NoFlagsBypasses(t *testing.T) {
	t.Parallel()
	extractor := &fakePostGenExtractor{}
	uc := NewPostGenUseCase(extractor, nil, nil, "llama3", zap.NewNop())
	res, err := uc.Run(context.Background(), &scriptpkg.GenerationSpec{ExtractEntities: false, GenerateMetadata: false}, "hello script body")
	require.NoError(t, err)
	assert.Empty(t, res.EntitiesJSON)
	assert.Nil(t, res.VideoMetadata)
	assert.Equal(t, int32(0), extractor.calls.Load(), "neither flag → extractor must not be called")
}

func TestPostGenUseCase_NilPayloadBypasses(t *testing.T) {
	t.Parallel()
	extractor := &fakePostGenExtractor{}
	uc := NewPostGenUseCase(extractor, nil, nil, "llama3", zap.NewNop())
	res, err := uc.Run(context.Background(), nil, "hello script body")
	require.NoError(t, err)
	assert.Empty(t, res.EntitiesJSON)
	assert.Nil(t, res.VideoMetadata)
	assert.Equal(t, int32(0), extractor.calls.Load(), "nil spec must not invoke the extractor")
}

func TestPostGenUseCase_ExtractorErrorIsLoggedNotReturned(t *testing.T) {
	t.Parallel()
	extractor := &fakePostGenExtractor{returnErr: errors.New("extractor boom")}
	uc := NewPostGenUseCase(extractor, nil, nil, "llama3", zap.NewNop())
	res, err := uc.Run(context.Background(), &scriptpkg.GenerationSpec{ExtractEntities: true}, "hello script body")
	require.NoError(t, err, "extractor errors must NOT propagate (best-effort semantics)")
	assert.Empty(t, res.EntitiesJSON, "EntitiesJSON must be empty on extractor error")
	assert.Equal(t, int32(1), extractor.calls.Load())
}

func TestPostGenUseCase_Run_TableDriven(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		extract      bool
		generateMeta bool
		payloadNil   bool
		expectExt    bool
		expectIns    bool
		expectMeta   bool
	}{
		{"both flags false bypasses both phases", false, false, false, false, false, false},
		{"nil payload bypasses both phases", true, true, true, false, false, false},
		{"extract only calls extractor + insight builder", true, false, false, true, true, false},
		{"both flags call both phases", true, true, false, true, true, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			extractor := &fakePostGenExtractor{}
			insight := &fakePostGenInsight{out: ScriptInsights{ImportantWords: []string{"alpha", "beta"}}}
			// PostGenUseCase now calls BuildMetadataLanguages + GenerateVideoMetadata
			// directly (metadata.go) — no function-port deps needed.
			// For the metadata phase we supply a nil generator (short-circuits
			// the actual Ollama call but the path is still exercised).
			uc := NewPostGenUseCase(
				extractor,
				insight,
				nil, // nil generator → metadata phase short-circuits (best-effort)
				"llama3",
				zap.NewNop(),
			)
			spec := &scriptpkg.GenerationSpec{ExtractEntities: tc.extract, GenerateMetadata: tc.generateMeta, Title: "My Title"}
			if tc.payloadNil {
				spec = nil
			}
			res, err := uc.Run(context.Background(), spec, "real script body for table test")
			require.NoError(t, err)

			if tc.expectExt {
				assert.Equal(t, int32(1), extractor.calls.Load(), "extractor must be called when ExtractEntities=true")
				assert.NotEmpty(t, res.EntitiesJSON, "EntitiesJSON must be non-empty on extractor success")
			} else {
				assert.Equal(t, int32(0), extractor.calls.Load(), "extractor must NOT be called in short-circuit")
				assert.Empty(t, res.EntitiesJSON)
			}
			if tc.expectIns {
				assert.Equal(t, int32(1), insight.calls.Load(), "insight builder must be called when ExtractEntities=true")
				require.NotEmpty(t, res.Insights.ImportantWords)
				assert.Equal(t, "alpha", res.Insights.ImportantWords[0])
			} else {
				assert.Equal(t, int32(0), insight.calls.Load(), "insight builder must NOT be called in short-circuit")
			}
			if tc.expectMeta {
				// With nil generator, metadata phase short-circuits silently
				assert.Nil(t, res.VideoMetadata, "nil generator → metadata phase skipped")
			} else {
				assert.Nil(t, res.VideoMetadata)
			}
		})
	}
}
