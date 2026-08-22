// Package usecase — extraction_callbacks_iface.go: ExtractionCallbacks
// inbound-port interface declaration.
//
// PR-GODOBJ-1 (July 2026): the interface declaration is isolated in its
// own file per godlike/06 SSOT (one canonical owner per fact) — the
// ExtractionCallbacks port is a distinct fact from the orchestration
// logic (extraction_service.go), validation helpers (extraction_request.go),
// destination resolvers (extraction_destination.go), fan-out dispatch
// (extraction_fanout.go), and stats/classifier (extraction_result.go).
//
// The 13-method mega-interface is intentionally preserved as-is for
// this PR — splitting it into per-capability ports is a FORWARD-POINTER
// (gated on the AUDIT-2026-07-02 PR-VO-* related work for the YouTube
// orchestrator). The PRIORITY for PR-GODOBJ-1 is "kill the legacy inline
// loop in extraction_service.go + split" — interface refactor is
// orthogonal and out of scope here.
//
// Implementations live on *Service (service.go:NewService wires
// svc.extraction = NewExtractionService(... svc) — methods in
// callbacks.go satisfy the interface contract).
package usecase

import (
	"context"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/lifecycle"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/dto"
	youtubeports "github.com/Marcuss-ops/PipelineGen/internal/application/youtube/ports"
)

// ExtractionCallbacks is implemented by the root *Service. It
// delegates each callback to the appropriate capability service or
// port, keeping the ExtractionService focused on per-segment
// orchestration.
//
// All types come from shared packages (types/, ports/, asset/,
// lifecycle/) — never from the root youtube/ or extraction/ package
// — to avoid import cycles and type incompatibilities.
type ExtractionCallbacks interface {
	// Metadata enrichment (→ metadata.Service).
	EnrichClip(ctx context.Context, clipID string, ym *youtubeports.DownloaderMetadata, force bool)

	// Search/info (→ search.Service).
	GetVideoInfo(ctx context.Context, url string) (*youtubeports.DownloaderMetadata, error)

	// Classification (→ classifyCategory).
	ClassifyCategory(ctx context.Context, title string) string

	// Clip cache (→ checkExistingClip).
	CheckExistingClip(ctx context.Context, req *youtubetypes.ExtractRequest, clipID string, item *youtubetypes.ExtractItem, outDir string) bool

	// Lifecycle (→ processLifecycle).
	ProcessLifecycle(ctx context.Context, metadata *lifecycle.FinalizeInput, localPath, fileHash string, item *youtubetypes.ExtractItem)

	// Auto-indexing (→ indexing.go).
	TriggerAutoIndexing(ctx context.Context, clipID string)
	IndexClip(ctx context.Context, clipID string) error
	EnrichSkippedClip(ctx context.Context, clipID, videoURL, videoID string)

	// Subtitles (→ subtitleFetcher).
	SliceSubtitles(ctx context.Context, videoID string, startSec, endSec int, outputPath string) error

	// Whisper (→ whisper port).
	TranscribeAudio(ctx context.Context, localPath string) (string, error)

	// Hash (→ hashSvc).
	SHA256File(path string) string
	SHA256String(data string) string
	MD5File(path string) string
	MD5String(data string) string

	// Drive upload (→ driveFolderMgr).
	DriveUploadFileIfChanged(ctx context.Context, localPath, folderID, filename, group, subject string) (*youtubeports.UploadResultDTO, bool, error)
	DriveGetOrCreateFolder(ctx context.Context, name, parentID string) (string, error)

	// Ollama (→ ollama port + semaphore).
	OllamaSimpleGenerate(ctx context.Context, model, prompt string, timeoutSec int, opts map[string]any) (string, error)

	// Concurrency semaphores.
	AcquireVideoExtractSem(ctx context.Context) (release func())
	AcquireOllamaSem(ctx context.Context) (release func())
}
