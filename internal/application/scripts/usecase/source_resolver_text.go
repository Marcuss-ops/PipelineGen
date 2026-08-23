// Package scripts — source_resolver_text.go resolves SourceText
// sources into a ResolvedSource. Text resolution is a pure assembly
// of the source fields; no external service calls are needed.
package usecase

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// TextSourceResolver resolves SourceText sources into ResolvedSource.
// It assembles the topic, source_text, and guidelines into a single
// source text block. No external I/O — this is a pure function.
type TextSourceResolver struct{}

// NewTextSourceResolver creates a TextSourceResolver.
func NewTextSourceResolver() *TextSourceResolver {
	return &TextSourceResolver{}
}

// Resolve assembles the text source fields into a ResolvedSource.
//
// PR 4 (June 2026): resolutionContext is accepted for signature parity
// with the other resolvers. The text resolver owns the editor's
// Guidelines field (it's a text-source concept) and doesn't read
// resolutionContext.Language/Tone/etc — those are clip-pipeline
// concerns. However, resolutionContext.Title is used as the
// document title fallback so the resolver still respects operator
// intent for purely-textual flows.
func (r *TextSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, resCtx scriptpkg.SourceResolutionContext) (*scriptpkg.ResolvedSource, error) {
	_ = ctx // reserved for future tracing

	topic := strings.TrimSpace(src.Topic)
	sourceText := strings.TrimSpace(src.SourceText)
	guidelines := strings.TrimSpace(src.Guidelines)

	if topic == "" && sourceText == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: resCtx.ItemID,
			Reason: "text source requires topic or source_text",
		}
	}

	// Title defaults to topic; resolutionContext.Title overrides when
	// present (caller-provided canonical title).
	title := topic
	if resCtx.Title != "" {
		title = resCtx.Title
	}
	if title == "" {
		title = "Untitled Script"
	}

	// Assemble source text block.
	var assembled strings.Builder
	if topic != "" {
		assembled.WriteString(fmt.Sprintf("Topic: %s\n", topic))
	}
	if sourceText != "" {
		assembled.WriteString(fmt.Sprintf("Source: %s\n", sourceText))
	}
	if guidelines != "" {
		assembled.WriteString(fmt.Sprintf("Guidelines: %s\n", guidelines))
	}

	resolved := &scriptpkg.ResolvedSource{
		Type:            scriptpkg.SourceText,
		Topic:           topic,
		Title:           title,
		SourceText:      assembled.String(),
		Language:        resCtx.Language,
		GroundingPolicy: src.GroundingPolicy,
	}
	resolved.Fingerprint = BuildClipFingerprint(src, nil)
	return resolved, nil
}
