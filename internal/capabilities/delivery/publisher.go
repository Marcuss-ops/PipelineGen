package delivery

import "context"

// Publisher is the canonical contract for uploading files to Google Drive.
// Every endpoint and every job that produces a file for Drive MUST go
// through this interface. The caller describes WHAT to publish via
// PublishRequest (DestinationKey + logical metadata); the Publisher
// resolves WHERE it lands on Drive (root folder + path hierarchy +
// folder creation + upload).
//
// Fase 3 Spina Dorsale audit (July 2026) — migration status:
//
//	Already using delivery.Publisher:
//	  - clips/reupload_usecase.go     → Publish(DestinationYouTubeClip, ...)
//	  - clips/upload/usecase.go       → Publish(DestinationYouTubeClip, ...)
//	  - clips/bulk_upload_worker.go   → Publish(DestinationYouTubeClip, ...)
//	  - soundeffect/handler.go        → Publish(DestinationSoundEffect, ...)
//	  - books (buildBooksService)     → Publisher injected at composition
//	  - ingest lifecycle services     → Publisher injected at composition
//	  - media processor               → Publisher injected at composition
//
//	Still bypassing delivery.Publisher (tracked by Pattern 12):
//	  - drive/store.go::UploadToDrive       → used by images package (P0-2 added Publisher fallback chain)
//	  - jobs/worker/runner.go               → assetClient.UploadFile (worker artifacts, different concern)
//
//	DestinationKey coverage (complete):
//	  YouTubeClip, Artlist, Stock, Image, Voiceover, Book, Script, SoundEffect
//
// The concrete implementation lives in
// internal/platform/drive/publisher.go.
//
// Architecture rule (June 2026):
//
//	A caller that has a Publisher MUST NOT also hold a reference to
//	drive.Uploader or drive.DriveFolderManagerAdapter. The Publisher
//	is the single canal for all Drive writes.
type Publisher interface {
	Publish(ctx context.Context, req PublishRequest) (*PublishResult, error)

	// ResolveFolder resolves the Drive folder for a destination without
	// uploading a file. Used by capabilities that need folder-only
	// resolution (Artlist, Script). Returns the resolved folder ID.
	ResolveFolder(ctx context.Context, req PublishRequest) (string, error)
}
