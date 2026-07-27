// Package scriptgeneration — builder.go is the canonical pure
// transformation layer that converts a validated request envelope
// into a typed GenerateRequest.
//
// Verdetto invariant:
//
//	func BuildGenerateRequest(env *GenerationEnvelopeV2, idempotencyKey string) (GenerateRequest, error)
//
// Zero I/O — no network, no database, no Google Drive. The builder
// is demoted from the original ingress registry which called
// TranslateScenes, RenderGoogleDocContent, and CreateGoogleDoc inline.
//
// The canonical caller is the HTTP handler (HandlerGenerate) which
// previously built a SubmitRequest directly from the envelope. After
// this change the handler calls:
//
//	req, err := scriptgeneration.BuildGenerateRequest(env, idempotencyKey)
//	// then: svc.Start(ctx, req)
//
// No Ollama, no Google Docs, no Drive. Pure field mapping only.
package scriptgeneration

import (
	"fmt"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
)

// BuildGenerateRequest is the sole canonical builder for
// GenerateRequest. It accepts a validated GenerationEnvelopeV2
// and produces the domain-level request with zero side effects.
//
// Verdetto rules:
//   - NO translation.TranslateScenes(ctx, ...)
//   - NO translation.RenderGoogleDocContent(...)
//   - NO docCreator.CreateGoogleDoc(...)
//   - NO Ollama, NO database, NO Drive
//   - Returns only struct-literal construction + field mapping
//
// Source of truth: the first item in the envelope defines the
// primary generation parameters. Multi-item envelopes are treated
// as batch — the primary item configures the top-level parameters
// and each item is handled independently downstream.
func BuildGenerateRequest(env *scriptpkg.GenerationEnvelopeV2, idempotencyKey string) (GenerateRequest, error) {
	if env == nil {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope is nil")
	}
	if len(env.Items) == 0 {
		return GenerateRequest{}, fmt.Errorf("scriptgeneration: envelope has no items")
	}

	item := env.Items[0]

	// Map SourceSpec → scriptgeneration.Source (pure field copy).
	source := Source{
		Type:       SourceType(item.Source.Type),
		Topic:      item.Source.Topic,
		SourceText: item.Source.SourceText,
		ClipIDs:    copyStrings(item.Source.ClipIDs),
		Query:      item.Source.Query,
		MaxClips:   item.Source.MaxClips,
	}

	// Map languages from the output spec.
	var languages []Language
	for _, lang := range item.Output.Languages {
		if lang != "" {
			languages = append(languages, Language(lang))
		}
	}

	// Source language defaults to the item's language if set,
	// otherwise "en" (the canonical safety default).
	sourceLang := Language(item.Language)
	if sourceLang == "" {
		sourceLang = "en"
	}

	// Documents are opt-in through the canonical docs object. The output
	// languages remain available for translation and are not a docs trigger.
	docsEnabled := item.Docs.Enabled
	docsLanguages := item.Docs.Languages
	docsFolderID := item.Docs.FolderID

	// Determine if rendering is enabled: render_video maps to
	// the output.generate_timeline or a future render flag.
	// For now, derive from GenerateTimeline.
	renderVideo := item.Output.GenerateTimeline

	return GenerateRequest{
		IdempotencyKey: idempotencyKey,
		Source:         source,
		SourceLanguage: sourceLang,
		Languages:      languages,
		RenderVideo:    renderVideo,
		Docs: DocumentsConfig{
			Enabled:   docsEnabled,
			Languages: toLanguages(docsLanguages),
			FolderID:  docsFolderID,
		},
		DocsEnabled:   docsEnabled,
		DriveFolderID: docsFolderID,
		Title:         item.Title,
		OutputName:    item.Title, // fallback: output name defaults to title
	}, nil
}

func toLanguages(src []string) []Language {
	if src == nil {
		return nil
	}
	dst := make([]Language, 0, len(src))
	for _, lang := range src {
		if lang != "" {
			dst = append(dst, Language(lang))
		}
	}
	return dst
}

// copyStrings returns a copy of the string slice (nil-safe).
func copyStrings(src []string) []string {
	if src == nil {
		return nil
	}
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}
