// Package scripts — source_resolver_text.go resolves SourceText
// sources into a ResolvedSource. Text resolution is a pure assembly
// of the source fields; no external service calls are needed.
package scripts

import (
	"context"
	"fmt"
	"strings"

	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/domain/script"
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
func (r *TextSourceResolver) Resolve(ctx context.Context, src scriptpkg.SourceSpec, itemID string) (*scriptpkg.ResolvedSource, error) {
	_ = ctx // reserved for future tracing

	topic := strings.TrimSpace(src.Topic)
	sourceText := strings.TrimSpace(src.SourceText)
	guidelines := strings.TrimSpace(src.Guidelines)

	if topic == "" && sourceText == "" {
		return nil, &scriptpkg.NoSourceError{
			ItemID: itemID,
			Reason: "text source requires topic or source_text",
		}
	}

	// Title defaults to topic. Callers should have already normalized
	// this, but we support the resolver being called independently.
	title := topic
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
		Type:       scriptpkg.SourceText,
		Topic:      topic,
		Title:      title,
		SourceText: assembled.String(),
		Language:   "", // filled by the caller from the normalized item
	}
	resolved.Fingerprint = computeSourceFingerprint(src, nil)
	return resolved, nil
}
