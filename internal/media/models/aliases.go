// Package models re-exports the canonical domain types from
// internal/domain/media for backward compatibility. The 40+ importers of
// "github.com/Marcuss-ops/PipelineGen/internal/media/models" keep working
// untouched; new code should import internal/domain/media directly.
//
// GenerationStyle and GenerationStyles are intentionally NOT aliased here
// because style.yaml loading lives in
// internal/media/generation/style_registry.go which still consumes them
// via this package. Once that consumer migrates, this file will shrink.
//
// This shim is the PR-C type-alias layer described in AGENTS.md §"Refactor".
package models

import "github.com/Marcuss-ops/PipelineGen/internal/domain/media"

// ── Type aliases ────────────────────────────────────────────────────
// `type X = media.X` is a true alias — methods on media.X carry over, and
// reflect sees them as the same type.

type (
	AssetNode            = media.AssetNode
	AssetExecutionResult = media.AssetExecutionResult
	ClipFolder           = media.ClipFolder
	ClipManifest         = media.ClipManifest
	ClipFolderStats      = media.ClipFolderStats
	ClipManifestItem     = media.ClipManifestItem
	IndexingCheckpoint   = media.IndexingCheckpoint
	PipelineStrategy     = media.PipelineStrategy
	MonitoredSource      = media.MonitoredSource

	SourceType  = media.SourceType
	MediaType   = media.MediaType
	AssetStatus = media.AssetStatus

	Subject      = media.Subject
	ImageAsset   = media.ImageAsset
	ImageUsage   = media.ImageUsage
	ImageTag     = media.ImageTag
	CategoryChannel = media.CategoryChannel
	SearchQuery      = media.SearchQuery
	SearchQueryResult = media.SearchQueryResult
)

// ── Const re-exports ─────────────────────────────────────────────────
// Go does not alias constants, so each value is re-declared in this
// package to keep callers using models.SourceStock (etc.) compiling
// unchanged.

const (
	SourceStock      = media.SourceStock
	SourceArtlist    = media.SourceArtlist
	SourceYoutubeClip = media.SourceYoutubeClip
	SourceClipDrive  = media.SourceClipDrive
	SourceImage      = media.SourceImage
	SourceGenerated  = media.SourceGenerated
)

const (
	MediaTypeStock    = media.MediaTypeStock
	MediaTypeClip     = media.MediaTypeClip
	MediaTypeImage    = media.MediaTypeImage
	MediaTypeAudio    = media.MediaTypeAudio
	MediaTypeDocument = media.MediaTypeDocument
)

const (
	AssetStatusActive     = media.AssetStatusActive
	AssetStatusArchived   = media.AssetStatusArchived
	AssetStatusDeleted    = media.AssetStatusDeleted
	AssetStatusProcessing = media.AssetStatusProcessing
	AssetStatusFailed     = media.AssetStatusFailed
)

const (
	StrategyVerify  = media.StrategyVerify
	StrategySkip    = media.StrategySkip
	StrategyReplace = media.StrategyReplace
)

// ── Function re-exports ─────────────────────────────────────────────
// Domain owns the implementation; this package forwards the call so that
// bytecode-level callers (`models.NormalizeStrategy(...)`) keep working.

func NormalizeStrategy(strategy string, force bool) PipelineStrategy {
	return media.NormalizeStrategy(strategy, force)
}

func ActiveKey(prefix, term, folderID string, strategy string, dryRun bool) string {
	return media.ActiveKey(prefix, term, folderID, strategy, dryRun)
}
