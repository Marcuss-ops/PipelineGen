package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	domaingeneration "github.com/Marcuss-ops/PipelineGen/internal/domain/generation"
)

// Normalizer converts external envelopes into a canonical in-memory shape.
type Normalizer interface {
	Normalize(ctx context.Context, env domaingeneration.GenerationEnvelopeV2) (domaingeneration.GenerationEnvelopeV2, error)
}

// Validator checks the normalized envelope before resolution.
type Validator interface {
	Validate(ctx context.Context, env domaingeneration.GenerationEnvelopeV2) error
}

// SourceResolver turns a normalized envelope into an executable plan.
type SourceResolver func(ctx context.Context, env domaingeneration.GenerationEnvelopeV2) (*domaingeneration.ResolvedGenerationPlan, error)

// SourceResolverRegistry stores source-specific resolvers.
type SourceResolverRegistry struct {
	resolvers map[domaingeneration.SourceKind]SourceResolver
}

func NewSourceResolverRegistry() *SourceResolverRegistry {
	return &SourceResolverRegistry{resolvers: make(map[domaingeneration.SourceKind]SourceResolver)}
}

func (r *SourceResolverRegistry) Register(kind domaingeneration.SourceKind, resolver SourceResolver) error {
	if r == nil {
		return fmt.Errorf("source resolver registry is nil")
	}
	if strings.TrimSpace(string(kind)) == "" {
		return fmt.Errorf("source kind is required")
	}
	if resolver == nil {
		return fmt.Errorf("resolver is required for %s", kind)
	}
	r.resolvers[kind] = resolver
	return nil
}

func (r *SourceResolverRegistry) Resolve(kind domaingeneration.SourceKind) (SourceResolver, bool) {
	if r == nil {
		return nil, false
	}
	resolver, ok := r.resolvers[kind]
	return resolver, ok
}

// PostProcessor transforms an engine result into the public result shape.
type PostProcessor func(ctx context.Context, plan *domaingeneration.ResolvedGenerationPlan, raw any) (*domaingeneration.GenerationResult, error)

// PostProcessorRegistry stores optional output transforms.
type PostProcessorRegistry struct {
	processors map[string]PostProcessor
}

func NewPostProcessorRegistry() *PostProcessorRegistry {
	return &PostProcessorRegistry{processors: make(map[string]PostProcessor)}
}

func (r *PostProcessorRegistry) Register(name string, processor PostProcessor) error {
	if r == nil {
		return fmt.Errorf("post-processor registry is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("post-processor name is required")
	}
	if processor == nil {
		return fmt.Errorf("post-processor is required for %s", name)
	}
	r.processors[name] = processor
	return nil
}

func (r *PostProcessorRegistry) Get(name string) (PostProcessor, bool) {
	if r == nil {
		return nil, false
	}
	processor, ok := r.processors[strings.TrimSpace(name)]
	return processor, ok
}

// Engine is the production execution port used by GenerateOneUseCase.
type Engine interface {
	Generate(ctx context.Context, plan *domaingeneration.ResolvedGenerationPlan) (any, error)
}

// GenerateOneUseCase orchestrates normalization, validation, resolution,
// engine execution and post-processing.
type GenerateOneUseCase struct {
	Normalizer  Normalizer
	Validator   Validator
	Resolvers   *SourceResolverRegistry
	Engine      Engine
	PostProcess *PostProcessorRegistry
}

func NewGenerateOneUseCase(normalizer Normalizer, validator Validator, resolvers *SourceResolverRegistry, engine Engine, postProcess *PostProcessorRegistry) *GenerateOneUseCase {
	return &GenerateOneUseCase{
		Normalizer:  normalizer,
		Validator:   validator,
		Resolvers:   resolvers,
		Engine:      engine,
		PostProcess: postProcess,
	}
}

func (u *GenerateOneUseCase) Execute(ctx context.Context, env domaingeneration.GenerationEnvelopeV2) (*domaingeneration.GenerationResult, error) {
	if u == nil {
		return nil, fmt.Errorf("generate-one use case is nil")
	}
	if u.Normalizer != nil {
		normalized, err := u.Normalizer.Normalize(ctx, env)
		if err != nil {
			return nil, err
		}
		env = normalized
	} else {
		env = normalizeEnvelopeV2(env)
	}
	if env.Version == 0 {
		env.Version = domaingeneration.EnvelopeVersionV2
	}

	if u.Validator != nil {
		if err := u.Validator.Validate(ctx, env); err != nil {
			return nil, err
		}
	} else if err := validateEnvelopeV2(env); err != nil {
		return nil, err
	}

	var plan *domaingeneration.ResolvedGenerationPlan
	if u.Resolvers != nil {
		resolver, ok := u.Resolvers.Resolve(env.Source)
		if !ok {
			return nil, fmt.Errorf("no source resolver registered for %s", env.Source)
		}
		var err error
		plan, err = resolver(ctx, env)
		if err != nil {
			return nil, err
		}
	}
	if plan == nil {
		plan = defaultResolvedPlan(env)
	}

	if u.Engine == nil {
		return nil, fmt.Errorf("generation engine is not configured")
	}
	raw, err := u.Engine.Generate(ctx, plan)
	if err != nil {
		return nil, err
	}

	if u.PostProcess != nil {
		if proc, ok := u.PostProcess.Get(string(env.Type)); ok {
			return proc(ctx, plan, raw)
		}
	}

	return &domaingeneration.GenerationResult{
		OK:     true,
		Type:   env.Type,
		Title:  plan.Title,
		Source: plan.Source,
		Result: raw,
	}, nil
}

type defaultNormalizer struct{}

func (defaultNormalizer) Normalize(_ context.Context, env domaingeneration.GenerationEnvelopeV2) (domaingeneration.GenerationEnvelopeV2, error) {
	return normalizeEnvelopeV2(env), nil
}

type defaultValidator struct{}

func (defaultValidator) Validate(_ context.Context, env domaingeneration.GenerationEnvelopeV2) error {
	return validateEnvelopeV2(env)
}

func normalizeEnvelopeV2(env domaingeneration.GenerationEnvelopeV2) domaingeneration.GenerationEnvelopeV2 {
	env.Version = domaingeneration.EnvelopeVersionV2
	env.Type = domaingeneration.Type(strings.TrimSpace(env.Type.String()))
	env.Source = domaingeneration.SourceKind(strings.TrimSpace(string(env.Source)))
	env.Title = strings.TrimSpace(env.Title)
	env.Language = strings.TrimSpace(env.Language)
	env.Tone = strings.TrimSpace(env.Tone)
	env.Style = strings.TrimSpace(env.Style)
	env.Model = strings.TrimSpace(env.Model)
	env.SourceText = strings.TrimSpace(env.SourceText)
	env.DriveFolderID = strings.TrimSpace(env.DriveFolderID)
	env.ClipIDs = cleanStrings(env.ClipIDs)
	env.PostProcess = cleanStrings(env.PostProcess)
	if env.Options == nil {
		env.Options = map[string]any{}
	}
	if env.Metadata == nil {
		env.Metadata = map[string]any{}
	}
	return env
}

func validateEnvelopeV2(env domaingeneration.GenerationEnvelopeV2) error {
	if strings.TrimSpace(env.Type.String()) == "" {
		return fmt.Errorf("generation type is required")
	}
	if strings.TrimSpace(string(env.Source)) == "" {
		return fmt.Errorf("generation source is required")
	}
	switch env.Source {
	case domaingeneration.SourceKindText:
		if strings.TrimSpace(env.SourceText) == "" && strings.TrimSpace(env.Title) == "" {
			return fmt.Errorf("source_text or title is required for text generation")
		}
	case domaingeneration.SourceKindClips:
		if len(env.ClipIDs) == 0 && strings.TrimSpace(env.SourceText) == "" {
			return fmt.Errorf("clip_ids or source_text is required for clip generation")
		}
	case domaingeneration.SourceKindCatalog, domaingeneration.SourceKindSearch, domaingeneration.SourceKindBatch:
		if strings.TrimSpace(env.SourceText) == "" && len(env.Items) == 0 {
			return fmt.Errorf("source_text or items is required for %s generation", env.Source)
		}
	}
	return nil
}

func defaultResolvedPlan(env domaingeneration.GenerationEnvelopeV2) *domaingeneration.ResolvedGenerationPlan {
	return &domaingeneration.ResolvedGenerationPlan{
		Type:          env.Type,
		Source:        env.Source,
		Title:         env.Title,
		Language:      env.Language,
		Tone:          env.Tone,
		Style:         env.Style,
		Model:         env.Model,
		SourceText:    env.SourceText,
		ClipIDs:       append([]string(nil), env.ClipIDs...),
		NumClips:      env.NumClips,
		DriveFolderID: env.DriveFolderID,
		Options:       cloneMap(env.Options),
		Items:         append([]domaingeneration.EnvelopeItem(nil), env.Items...),
		PostProcess:   append([]string(nil), env.PostProcess...),
		Metadata:      cloneMap(env.Metadata),
	}
}

func cloneMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// BuildDefaultNormalizer returns the default no-op normalizer.
func BuildDefaultNormalizer() Normalizer { return defaultNormalizer{} }

// BuildDefaultValidator returns the default validator.
func BuildDefaultValidator() Validator { return defaultValidator{} }

// MarshalEnvelope renders the unified envelope to JSON.
func MarshalEnvelope(env domaingeneration.GenerationEnvelopeV2) ([]byte, error) {
	env = normalizeEnvelopeV2(env)
	return json.Marshal(env)
}
