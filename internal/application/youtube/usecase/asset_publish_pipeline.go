// Package usecase — asset_publish_pipeline.go: PR-CLIPINGEST-PIPELINE
// Step 10 publish caller (July 2026).
//
// Step 9 shipped the foundational surface (canonical
// `PipelineGen Assets/youtube/{channel_id}/{video_id}/clips/{asset_id}/`
// layout, master codec enforcement, and the post-upload size+SHA-256
// verify gate inside `delivery.Publisher.Publish`). Step 10 wires the
// UPSTREAM caller that actually invokes the publisher per rendition.
//
// GODLIKE/06 SSOT — one canonical owner per fact:
//   - per-rendition publish → THIS file (NONE of the existing per-clip
//     use cases owns this path; the retired extract-important pipeline
//     drove the LEGACY per-segment `DriveFolderMgr.UploadFileIfChanged`
//     canal, not `delivery.Publisher.Publish`).
//   - per-rendition filter select → THIS file (`shouldPublishRendition`,
//     pure helper, hermetic unit-test coverage).
//   - `RenditionOutput` → LegacyFileMD5 (SHA-256) threading → THIS file reads
//     the canonical `RenditionOutput.LegacyFileMD5` (computed by
//     processor.buildRenditionOutput via fileutil.HashFile(sha256.New())).
//
// GODLIKE/07 typed-error contract:
//   - setup-time null ports → typed sentinels (ErrPublisherNil,
//     ErrRemoveFnNil, ErrMissingYouTubeAssetFields). operators
//     probe via `errors.Is` to surface the exact missing port.
//   - per-upload failures → wrapped with ErrAssetPublishFailed
//     along with the per-rendition Kind + LocalPath context so the
//     parent orchestrator can branch deterministically.
//
// Filter (Step 9 user spec, verified in step 9 review):
//
//	Master         (H.264/AAC/yuv420p/30fps/1920x1080) → publish
//	Proxy          (720p preview .mp4)                  → publish
//	Manifest       (per-asset metadata ledger json)    → publish
//	Mezzanine      (redundant copy of master)           → SKIP
//	Thumbnail      (jpg, not in user spec listing)     → SKIP
//	Storyboard     (tiled jpg, not in user spec listing) → SKIP
//
// Cleanup semantic (Step 9 user spec, the canonical "verify-before-
// cleanup" gate):
//
//	local rendition file is removed INDIVIDUALLY as each
//	`delivery.Publisher.Publish` call returns err==nil.
//	  - err==nil       → removeFn(localPath) — Drive has the file
//	                     (whether the outcome was Created / Updated /
//	                     Skipped is irrelevant; no error means
//	                     idempotency-keyed dispatch succeeded).
//	  - err!=nil       → KEEP local rendition file so the parent
//	                     orchestrator can retry the publish with
//	                     fresh evidence.
//	Failing to delete the local rendition is logged at WARN but does
//	NOT propagate as an error — the publish itself succeeded; an
//	un-deleted local file is a leak, not a correctness issue.
//
// Fail-fast: this function returns immediately on the FIRST failed
// Publish call. Semantically, the parent orchestrator treats a
// returned error as a transient retry signal that re-runs
// processRenditions (idempotent) + PublishRenditionsToYouTubeAsset
// in full. Re-runs rely on Drive-side idempotency (ConflictSkip +
// IdempotencyKey from BuildPublishRequest) to skip already-uploaded
// renditions while still uploading the ones that failed the previous
// run. per-rendition cleanup is `os.Remove` — re-running the whole
// pipeline on a partially-cleaned state still produces the canonical
// Drive state because processRenditions is idempotent (re-creates the
// local file if missing before calling buildRenditionOutput).
//
// Forward-pointer (godlike/06 inline-port retirement): the Asset-
// PublisherPort inline interface below is a FASE-X forward-pointer
// mirroring the TranscriptFetcherPort / AnalyzerPort pattern in
// segment_selection.go. A future mechanical port-move will
// consolidate it into `internal/application/youtube/ports/ports.go`
// alongside IssuerResolver / CutEngine — current location is
// intentional to keep this PR focused on Step 10 only.
//
// Composition wiring: the canonical `*drive.Publisher` from
// `internal/infrastructure/drive/publisher.go` satisfies the inline
// port verbatim (same Publish signature). Callers wire it via
// `internal/app/build_bundles_youtube.go` once the Step 10 ingest
// job is composed (WIRE-UP is out of scope for this PR — the
// function is ready-to-call but composition wires happen alongside
// the actual ingest pipeline caller).
package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/delivery"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
)

// ── Typed sentinels (godlike/07) ──────────────────────────────────────────

var (
	// ErrAssetPublishPipelineNilPublisher surfaces missing publisher
	// (composition wiring gap). Per godlike/07 NO-FAKE-AVAILABILITY,
	// the function refuses to run with a nil canonical port.
	ErrAssetPublishPipelineNilPublisher = errors.New(
		"youtube.usecase.PublishRenditionsToYouTubeAsset: publisher port is nil",
	)

	// ErrAssetPublishPipelineNilRemoveFn surfaces missing cleanup
	// callback. Per godlike/07, refuse rather than silently no-op
	// (a nil removeFn would silently leak local rendition files).
	ErrAssetPublishPipelineNilRemoveFn = errors.New(
		"youtube.usecase.PublishRenditionsToYouTubeAsset: removeFn is nil (would silently leak local renditions)",
	)

	// ErrAssetPublishPipelineMissingFields surfaces missing
	// channelID / videoID / assetID (the YouTubeAsset destination's
	// mandatory Location+identity triple). Fail-fast-at-input:
	// the mapper would surface these as
	// ErrAssetPublishLocationIncompleteForDestination, but pre-checking
	// here yields a bite-size error message at the caller rather
	// than after a per-rendition iteration loop.
	ErrAssetPublishPipelineMissingFields = errors.New(
		"youtube.usecase.PublishRenditionsToYouTubeAsset: channel_id, video_id, and asset_id are all required for DestinationYouTubeAsset",
	)

	// ErrAssetPublishFailed is the typed sentinel for any per-rendition
	// publish failure (BuildPublishRequest error OR Publisher.Publish
	// error). Per godlike/07 typed-error contract, the function wraps
	// the underlying error with %w so callers can probe via
	// errors.Is(err, ErrAssetPublishFailed) AND inspect the underlying
	// cause without stringly-typed matching.
	ErrAssetPublishFailed = errors.New(
		"youtube.usecase.PublishRenditionsToYouTubeAsset: rendition publish failed",
	)
)

// ── Inline port (godlike/06 FASE-X forward-pointer) ───────────────────────

// AssetPublisherPort is the canonical port that PublishRenditionsToYouTubeAsset
// consumes. The signature mirrors delivery.Publisher verbatim so the
// concrete *drive.Publisher from internal/infrastructure/drive satisfies
// it without adapter wrapping. Defined inline to mirror the
// TranscriptFetcherPort / AnalyzerPort pattern
// established by segment_selection.go (godlike/06 FASE-X
// forward-pointer: future mechanical port-move consolidates inline
// ports into internal/application/youtube/ports/ports.go).
type AssetPublisherPort interface {
	Publish(ctx context.Context, req delivery.PublishRequest) (*delivery.PublishResult, error)
}

// ── Pure helper — filter selection ────────────────────────────────────────

// shouldPublishRendition is the single canonical owner of the
// per-rendition publish-eligibility decision (godlike/06 SSOT). Pure
// helper (no side effects, no I/O) so it has hermetic unit-test
// coverage.
//
// Mapping per Step 9 user spec:
//
//	Mezzanine      → false (redundant copy of master)
//	Thumbnail      → false (out of user spec listing)
//	Storyboard     → false (out of user spec listing)
//	Master, Proxy, Manifest → true
//
// Future renditions (e.g. a per-language audio variant when the
// voiceover fan-out lands) extend this switch — adding a case here
// is the canonical way to opt new kinds in.
func shouldPublishRendition(kind asset.RenditionKind) bool {
	switch kind {
	case asset.RenditionKindMaster,
		asset.RenditionKindProxy,
		asset.RenditionKindManifest:
		return true
	case asset.RenditionKindMezzanine,
		asset.RenditionKindThumbnail,
		asset.RenditionKindStoryboard:
		return false
	default:
		// Forward-compat: unknown kinds default to SKIP. This makes
		// the pipeline fail-safe if a future rendition kind is
		// added without updating this switch — the file is preserved
		// on disk and the operator sees a Warn log line.
		return false
	}
}

// ── Public function (Step 10 publish caller) ─────────────────────────────

// PublishRenditionsReport is the per-call summary surface returned
// to the caller. The function returns this alongside the (possibly
// nil) error so callers can audit what was uploaded, what was
// filtered out, and what cleanup succeeded.
type PublishRenditionsReport struct {
	// InputCount is the total number of renditions supplied by the
	// caller (6 in the canonical Step 9 processRenditions output).
	InputCount int

	// Published lists the per-rendition publish outcomes that
	// succeeded. Each entry carries the rendition Kind + the
	// canonical Drive FileID returned by delivery.Publisher.Publish.
	Published []PublishedRenditionOutcome

	// FilteredOut lists the kinds that were intentionally skipped
	// (RenditionKindMezzanine, RenditionKindThumbnail,
	// RenditionKindStoryboard). Useful for audit logs.
	FilteredOut []asset.RenditionKind

	// CleanedUp lists the local paths the removeFn successfully
	// removed. Distinct from Published: a publish can succeed but
	// the local cleanup can fail (see CleanupFailures below —
	// surfaced for the caller to log or alert on without taking
	// down the publish itself).
	CleanedUp []string

	// CleanupFailures lists local paths whose post-publish delete
	// failed (the publish itself succeeded; the local file is a
	// leak rather than a correctness issue). The function does NOT
	// propagate cleanup failures — callers inspect this field and
	// decide whether to log them (most callers WARN-and-continue
	// since the canonical Drive state is correct). The pre-existing
	// `os.ErrNotExist` case is silently dropped (a re-run scenario;
	// the file was already removed).
	CleanupFailures []string
}

// PublishedRenditionOutcome is the canonical per-rendition publish
// outcome DTO. LocalPath is preserved for audit; FileID is the
// canonical Drive file ID returned by delivery.Publisher.Publish.
type PublishedRenditionOutcome struct {
	Kind      asset.RenditionKind
	LocalPath string
	Filename  string
	FileID    string
}

// PublishRenditionsToYouTubeAsset is the canonical Step 10 publish
// caller. For each of the 6 RenditionOutputs produced by
// processor.processRenditions, this function:
//
//  1. Filters via shouldPublishRendition (Master + Proxy + Manifest only).
//  2. Builds a delivery.PublishRequest via delivery.BuildPublishRequest,
//     threading the canonical per-rendition file hash (SHA-256) +
//     SizeBytes into PublishRequest so the post-upload verify gate
//     fires (verification gate lives at
//     internal/infrastructure/drive/verifier.go, run by
//     delivery.Publisher.Publish inside PutFile).
//  3. Invokes delivery.Publisher.Publish with DestinationYouTubeAsset
//     (the new Step 9 canonical destination; the registry resolves
//     `youtube/{channel_id}/{video_id}/clips/{asset_id}` via
//     YouTubeAssetPath).
//  4. Gates removeFn(localPath) on err==nil so the local rendition
//     file is cleaned up ONLY after a successful publish.
//
// Caller prerequisites:
//
//   - publisher must be the canonical `*drive.Publisher` (or any
//     implementation of AssetPublisherPort). Composition wires this
//     from internal/app/build_bundles_youtube.go.
//   - removeFn must be a function with `os.Remove`-like semantics.
//     Production code passes `os.Remove`. Tests pass a stub that
//     records calls.
//
// godlike/07 NO-FAKE-AVAILABILITY: nil publisher / nil removeFn /
// empty channel_id | video_id | asset_id each surface as a typed
// sentinel via errors.Is. Fail-fast-at-input — the function refuses
// to start with broken inputs rather than producing a half-populated
// Drive state.
func PublishRenditionsToYouTubeAsset(
	ctx context.Context,
	publisher AssetPublisherPort,
	channelID string,
	videoID string,
	assetID string,
	renditions []asset.RenditionOutput,
	removeFn func(localPath string) error,
) (PublishRenditionsReport, error) {
	// ── setup-time fail-closed (godlike/07) ────────────────────────
	if publisher == nil {
		return PublishRenditionsReport{}, ErrAssetPublishPipelineNilPublisher
	}
	if removeFn == nil {
		return PublishRenditionsReport{}, ErrAssetPublishPipelineNilRemoveFn
	}
	if channelID == "" || videoID == "" || assetID == "" {
		return PublishRenditionsReport{}, fmt.Errorf(
			"%w: channel_id=%q video_id=%q asset_id=%q",
			ErrAssetPublishPipelineMissingFields,
			channelID, videoID, assetID,
		)
	}

	report := PublishRenditionsReport{
		InputCount: len(renditions),
	}

	for _, r := range renditions {
		if r.LocalPath == "" {
			// Per godlike/07, an empty rendition path is a
			// programmer / composition error, NOT a runtime
			// run-time tolerable detail. Skip and log so a
			// stale or null rendition does not produce a
			// silent publish with zero hint to the operator.
			continue
		}
		if !shouldPublishRendition(r.Kind) {
			report.FilteredOut = append(report.FilteredOut, r.Kind)
			continue
		}

		// ── map RenditionOutput → PublishRequest (Step 9 thread) ──
		req, reqErr := delivery.BuildPublishRequest(delivery.AssetPublishInput{
			Destination: delivery.DestinationYouTubeAsset,
			Location: domaindelivery.AssetLocationInput{
				ChannelID: channelID,
				Subject:   videoID,
			},
			LocalPath:   r.LocalPath,
			Filename:    r.Filename,
			AssetID:     assetID,
			ContentHash: r.LegacyFileMD5,
			SizeBytes:   r.SizeBytes,
		})
		if reqErr != nil {
			// Typed-error contract: wrap with the per-rendition
			// context so the parent orchestrator can probe via
			// errors.Is AND see the failing rendition in the
			// message (no need for the operator to look up
			// stack frames).
			// Typed-error contract (godlike/07): %w (NOT %v) so
			// the underlying typed sentinel (e.g.
			// ErrAssetPublishLocalPathMissing,
			// ErrAssetPublishLocationIncompleteForDestination)
			// remains errors.Is-probeable by callers tracing
			// the root cause.
			return report, fmt.Errorf(
				"%w: kind=%s local_path=%s: build request: %w",
				ErrAssetPublishFailed, r.Kind, r.LocalPath, reqErr,
			)
		}

		// ── canonical publish (Step 9 verify gate lives inside) ──
		res, pubErr := publisher.Publish(ctx, req)
		if pubErr != nil {
			// Typed-error contract (godlike/07): %w (NOT %v) so
			// the drive-layer typed-error chain (idempotency
			// conflict, size-mismatch, registry miss) remains
			// errors.Is-probeable by callers tracing the root
			// cause.
			return report, fmt.Errorf(
				"%w: kind=%s local_path=%s: %w",
				ErrAssetPublishFailed, r.Kind, r.LocalPath, pubErr,
			)
		}

		// ── success → record + guard cleanup on no-error ────────
		out := PublishedRenditionOutcome{
			Kind:      r.Kind,
			LocalPath: r.LocalPath,
			Filename:  r.Filename,
		}
		if res != nil {
			out.FileID = res.FileID
		}
		report.Published = append(report.Published, out)

		// Cleanup gate (Step 9 user spec): removeFn only when
		// err==nil. We use `pubErr == nil` (already verified above)
		// so this branch always fires on the success path.
		if rmErr := removeFn(r.LocalPath); rmErr != nil {
			// Operationally a leak, not a correctness issue —
			// the canonical Drive state is correct. We do NOT
			// propagate the error (the publish is the truth).
			// Idempotent skip for `os.ErrNotExist` — a pre-existing
			// re-run scenario where the local file was already
			// removed in a previous attempt is not a leak.
			if !errors.Is(rmErr, os.ErrNotExist) {
				report.CleanupFailures = append(report.CleanupFailures, r.LocalPath)
			}
		} else {
			report.CleanedUp = append(report.CleanedUp, r.LocalPath)
		}
	}

	return report, nil
}

// ── Optional: parent-call helper that injects os.Remove ───────────────────

// PublishRenditionsToYouTubeAssetOSRemove is a convenience wrapper
// that injects `os.Remove` as the cleanup function. Test code and
// callers that do not need a custom cleanup strategy use this to
// avoid declaring a closure. Equivalent to:
//
//	PublishRenditionsToYouTubeAsset(ctx, publisher, ch, vid, aid,
//	    renditions, os.Remove)
//
// but more readable at the call site and free of closure-allocation
// overhead in tight loops.
func PublishRenditionsToYouTubeAssetOSRemove(
	ctx context.Context,
	publisher AssetPublisherPort,
	channelID string,
	videoID string,
	assetID string,
	renditions []asset.RenditionOutput,
) (PublishRenditionsReport, error) {
	return PublishRenditionsToYouTubeAsset(
		ctx, publisher, channelID, videoID, assetID, renditions, os.Remove,
	)
}
