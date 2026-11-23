package audio

// deps.go owns the typed input contracts of the audio module.
//
// NewSoundEffectAdapters took four positional arguments where three of them are
// ports/repositories reached through different layers and the fourth is a bare
// root path. A named struct keeps the destination root from being mistaken for
// a repository and makes the composition site read like the adapter it builds
// (BON-03 step B).

import (
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/ai/semantic"
	assets "github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/assets/channels"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/sqlite/outbox"
)

// SoundEffectAdaptersDeps is the input contract of NewSoundEffectAdapters.
// MediaRoot is the canonical Drive media root the destination resolver
// prefixes; it stays a separate field from the repositories on purpose.
type SoundEffectAdaptersDeps struct {
	Clips      *assets.ClipsRepository
	Metadata   semantic.MetadataWriterPort
	Dispatcher *outbox.Dispatcher
	MediaRoot  string
}
