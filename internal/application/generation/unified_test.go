package generation

import (
	"context"
	"testing"

	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
	"github.com/stretchr/testify/require"
)

type fakeEngine struct {
	got *domaingeneration.ResolvedGenerationPlan
}

func (f *fakeEngine) Generate(_ context.Context, plan *domaingeneration.ResolvedGenerationPlan) (any, error) {
	f.got = plan
	return map[string]any{"script": "ok"}, nil
}

func TestGenerateOneUseCase_NormalizesAndResolves(t *testing.T) {
	resolvers := NewSourceResolverRegistry()
	require.NoError(t, resolvers.Register(domaingeneration.SourceKindClips, func(_ context.Context, env domaingeneration.GenerationEnvelopeV2) (*domaingeneration.ResolvedGenerationPlan, error) {
		return &domaingeneration.ResolvedGenerationPlan{
			Type:    env.Type,
			Source:  env.Source,
			Title:   env.Title,
			ClipIDs: append([]string(nil), env.ClipIDs...),
		}, nil
	}))

	engine := &fakeEngine{}
	uc := NewGenerateOneUseCase(BuildDefaultNormalizer(), BuildDefaultValidator(), resolvers, engine, nil)

	result, err := uc.Execute(context.Background(), domaingeneration.GenerationEnvelopeV2{
		Type:    domaingeneration.TypeScriptFromClips,
		Source:  domaingeneration.SourceKindClips,
		Title:   "  Jackie Chan Interview  ",
		ClipIDs: []string{"  clip-1  "},
	})
	require.NoError(t, err)
	require.True(t, result.OK)
	require.Equal(t, "Jackie Chan Interview", engine.got.Title)
	require.Equal(t, []string{"clip-1"}, engine.got.ClipIDs)
	require.Equal(t, domaingeneration.TypeScriptFromClips, result.Type)
	require.Equal(t, domaingeneration.SourceKindClips, result.Source)
}

func TestMarshalEnvelopeSetsVersionAndTrims(t *testing.T) {
	raw, err := MarshalEnvelope(domaingeneration.GenerationEnvelopeV2{
		Type:       domaingeneration.TypeScriptFromClips,
		Source:     domaingeneration.SourceKindText,
		Title:      "  Title  ",
		SourceText: "  Hello  ",
	})
	require.NoError(t, err)
	require.Contains(t, string(raw), `"version":2`)
	require.Contains(t, string(raw), `"title":"Title"`)
	require.Contains(t, string(raw), `"source_text":"Hello"`)
}
