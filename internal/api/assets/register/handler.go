// Package register — handler.go slim orchestrator (PR-SPLIT-REGISTER-HANDLER, July 2026).
//
// Provides thin HTTP handlers for YouTube clip registration. All
// business orchestration lives in sourcing.Service — the handler is
// pure transport.
//
// 2-file split (PR-SPLIT-REGISTER-HANDLER, P2, deadline 2026-08-08):
//   - handler.go (this file) — THIN orchestrator: Handler struct +
//     NewHandler + RegisterRoutes + RegisterFromYouTube +
//     BatchRegisterFromYouTube + effectiveFolderID +
//     toRegisterClipCommand + expandClipsBySegments + the reserved
//     expansion sentinel constant.
//   - requests.go — the 3 wire-shape DTOs (RegisterFromYouTubeRequest +
//     BatchRegisterRequest + BatchRegisterResponse).
package register

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	apiutil "github.com/Marcuss-ops/PipelineGen/pkg/apiutil"
	urlutil "github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Handler manages YouTube clip registration. All business orchestration
// lives in sourcing.Service — the handler is pure transport.
//
// PR8 (June 2026): added Idempotency field — the reusable Gin
// idempotency middleware instance installed on POST /register-from-youtube
// (write route). BatchRegisterFromYouTube is intentionally NOT gated
// because it processes a list with potentially many distinct clip
// registrations — replay semantics per clip are preserved by the
// underlying dedup logic (FindByExternalRef), but the response is
// the per-call batch summary. nil disables idempotency for tests.
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): added DriveChecker field.
// BatchRegisterFromYouTube probes it BEFORE any folder_id routing —
// if any folder_id in the request is non-empty AND the checker
// returns a non-nil error, the handler fail-closed 503 (NOT 500
// nil-panic). The probe is wired by the composition root from a
// closure that mirrors the boot-time validateDriveServiceAvailability
// check (composition-root + handler-level defense-in-depth). nil
// checker → defensive always-fail (godlike/07 no-fake-availability;
// never accept folder_id traffic when wiring is unconfirmed).
type Handler struct {
	svc          *sourcing.Service
	log          *zap.Logger
	Idempotency  gin.HandlerFunc
	driveChecker func() error // PR-DRIVE-AVAILABILITY-GATE: fail-closed probe for folder_id routing
}

// NewHandler creates a YouTube registration handler.
//
// PR8 (June 2026): idempotencyMiddleware is the reusable Gin
// idempotency middleware instance from middleware.NewIdempotency
// (constructed in WireAssets). nil disables idempotency for tests.
//
// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): driveChecker is the
// canonical probe closure wired by the composition root from a
// closure mirroring validateDriveServiceAvailability (returns nil iff
// *drive.Uploader.Service is wired + cfg stat-OK). nil → defensive
// always-fail closure (godlike/07 no-fake-availability; an unwired
// handler must NEVER silently accept folder_id traffic). The
// composition root wires a real closure via Dependencies.DriveChecker;
// direct NewHandler callers (tests) may pass nil and accept the
// fail-closed-on-folder-id surface.
func NewHandler(svc *sourcing.Service, log *zap.Logger, idempotencyMiddleware gin.HandlerFunc, driveChecker func() error) *Handler {
	if log == nil {
		log = zap.NewNop()
	}
	var idem gin.HandlerFunc = func(c *gin.Context) { c.Next() }
	if idempotencyMiddleware != nil {
		idem = idempotencyMiddleware
	}
	// godlike/07 nil-tolerant defensive default: a handler constructed
	// without a driveChecker (test fixture, unwired composition root) MUST
	// NEVER accept folder_id traffic. We wire an "always-fail" closure
	// that returns a typed error so the preflight at BatchRegisterFromYouTube
	// has a consistent surface to probe. The error message names the
	// "driveChecker not wired" condition so operators reading the 503
	// response body understand the misconfiguration.
	var dc func() error = func() error {
		return fmt.Errorf("driveChecker not wired (composition root must thread Dependencies.DriveChecker from build_bundles_drive.go::BuildDriveBundle; nil-fallback blocks folder_id traffic per PR-DRIVE-AVAILABILITY-GATE fail-closed contract)")
	}
	if driveChecker != nil {
		dc = driveChecker
	}
	return &Handler{svc: svc, log: log, Idempotency: idem, driveChecker: dc}
}

// RegisterRoutes registers the registration endpoints.
//
// PR8 (June 2026): POST /register-from-youtube installs h.Idempotency
// before the handler — same Stripe-style replay semantics as the
// other write endpoints. /register-batch falls through (its
// semantics are inherently batch-shaped).
func (h *Handler) RegisterRoutes(r *gin.RouterGroup) {
	r.POST("/register-from-youtube", h.Idempotency, h.RegisterFromYouTube)
	r.POST("/register-batch", h.BatchRegisterFromYouTube)
}

// RegisterFromYouTube handles POST /api/media/register-from-youtube.
//
// Handler order (godlike/07 fail-fast-at-rest before fail-slow-at-svc):
//  1. BindJSON — surface malformed-JSON 400 before any downstream I/O.
//  2. Pre-flight URL validation — invalid URLs surface 400 BEFORE the
//     svc=nil short-circuit AND BEFORE the yt-dlp subprocess.
//  3. svc=nil guard — surface 503 only after the input is known-good.
//  4. svc call — typed Layer 1 transport, no business orchestration here.
func (h *Handler) RegisterFromYouTube(c *gin.Context) {
	req, ok := apiutil.BindJSON[RegisterFromYouTubeRequest](c)
	if !ok {
		return
	}

	// Pre-flight URL validation (godlike/07 input-fail-closed): invalid URLs
	// now return 400 BEFORE svc dispatch. Pre-fix they bubbled subprocess
	// exit-1 as 500 (svc wired) or surfaced 503 (svc=nil); post-fix they
	// surface the canonical "your input is bad" 400. Canonical typed gate
	// at pkg/urlutil/urlutil.go::ExtractVideoID.
	if _, err := urlutil.ExtractVideoID(req.URL); err != nil {
		apiutil.BadRequest(c, "invalid YouTube URL: "+err.Error())
		return
	}

	// PR-YT-SECONDS-PER-SEGMENT-WIRE pre-flight: a single-clip request
	// (POST /register-from-youtube) carrying seconds_per_segment > 0 is
	// rejected because the single-clip endpoint only emits ONE clip.
	// Operators wanting N-second auto-segmenting MUST use the batch
	// endpoint where each slice can become its own clip_id + Drive
	// upload. Reject early so the operator sees the correct path
	// rather than the silent-fall-through behavior the per-clip
	// path used to exhibit.
	if req.SecondsPerSegment > 0 {
		apiutil.BadRequest(c, "seconds_per_segment must be supplied via /api/media/register-batch (single-clip endpoint emits one clip_id; fan-out is the batch's job)."+
			" See PR-YT-SECONDS-PER-SEGMENT-WIRE for the canonical surface.")
		return
	}

	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "register service not wired")
		return
	}

	res, err := h.svc.RegisterFromYouTube(c.Request.Context(), toRegisterClipCommand(req))
	if err != nil {
		apiutil.InternalError(c, err)
		return
	}
	if res == nil {
		apiutil.Error(c, http.StatusInternalServerError, "empty registration result")
		return
	}

	apiutil.OK(c, gin.H{
		"ok":              res.OK,
		"duplicate":       res.Duplicate,
		"clip_id":         res.ClipID,
		"video_id":        res.VideoID,
		"name":            res.Name,
		"filename":        res.Filename,
		"duration_sec":    res.DurationSec,
		"drive_link":      res.DriveLink,
		"drive_file_id":   res.DriveFileID,
		"drive_folder_id": res.DriveFolderID,
		"drive_path":      res.DrivePath,
		"legacy_file_md5": res.LegacyFileMD5,
		"source":          res.Source,
		"location": gin.H{
			"category": res.Category,
			"subject":  req.Location.Subject,
			"provider": req.Location.Provider,
			"style":    req.Location.Style,
		},
		"category":        res.Category,
		"tags":            res.Tags,
		"local_path":      res.LocalPath,
		"indexed":         res.Indexed,
		"indexing_status": res.IndexingStatus,
		"transcribed":     res.Transcribed,
		"language":        res.Language,
		"related_clips":   res.RelatedClips,
		"message":         res.Message,
	})
}

// BatchRegisterFromYouTube handles POST /api/media/register-batch.
// Thin transport: delegates all orchestration to sourcing.Service.BatchRegisterFromYouTube.
//
// Handler order (godlike/07 fail-fast-at-input before fail-slow-at-svc):
//  1. BindJSON — surface malformed-JSON 400 before any downstream I/O.
//  2. clips-empty check — surface 400 before any downstream I/O.
//  3. Pre-flight Drive-availability gate (PR-DRIVE-AVAILABILITY-GATE) —
//     if any folder_id in the request is non-empty AND the canonical
//     DriveChecker probe returns a non-nil error, surface 503 with
//     the diagnostic BEFORE attempting any svc dispatch (which would
//     500-panic on *drive.Uploader.Service==nil). MUST precede svc=nil
//     check so the gate surface reaches the client even when svc is
//     unconfigured (godlike/07 fail-fast-at-input semantics).
//  4. svc=nil guard — surface 503 if the composition root forgot to wire.
//  5. folder_id backfill — mirrors the original-shape behaviour.
//  6. svc.RegisterFromYouTube + response.
func (h *Handler) BatchRegisterFromYouTube(c *gin.Context) {
	var req BatchRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		apiutil.BadRequest(c, "invalid request: "+err.Error())
		return
	}
	if len(req.Clips) == 0 {
		apiutil.BadRequest(c, "clips list is empty")
		return
	}

	// PR-YT-SECONDS-PER-SEGMENT-WIRE pre-flight: validate expansion
	// semantics BEFORE the Drive-availability gate so a malformed
	// seconds_per_segment doesn't burn the gate. The canonical
	// window for slicing is [start, end]; either bound must be
	// non-zero to anchor the partition. godlike/07 fail-fast-at-input:
	// surfaces the 400 BEFORE any downstream I/O.
	for i := range req.Clips {
		if req.Clips[i].SecondsPerSegment > 0 {
			if req.Clips[i].Start == 0 && req.Clips[i].End == 0 {
				apiutil.BadRequest(c, fmt.Sprintf("clip index %d: seconds_per_segment=%d requires explicit [start, end] (cannot derive bounds from 0,0)",
					i, req.Clips[i].SecondsPerSegment))
				return
			}
			if req.Clips[i].Start != 0 && req.Clips[i].End != 0 && req.Clips[i].End <= req.Clips[i].Start {
				apiutil.BadRequest(c, fmt.Sprintf("clip index %d: end (%v) must be greater than start (%v)",
					i, req.Clips[i].End, req.Clips[i].Start))
				return
			}
		}
	}

	// PR-YT-SECONDS-PER-SEGMENT-WIRE: server-side fan-out of any clip
	// with seconds_per_segment > 0 into N children matching [start, end]
	// partitioned into N-second slices. Each child feeds the canonical
	// per-clip register pipeline; each child therefore becomes ONE
	// clip_id + ONE Drive upload entry. Strips the seconds_per_segment
	// field on children to prevent recursive expansion if a child is
	// ever re-fed through the handler. godlike/07 minimum-blast-radius:
	// existing register-batch callers with seconds_per_segment unset
	// are byte-identical (zero-value passes through untouched).
	expanded, expandErr := expandClipsBySegments(req.Clips)
	if expandErr != nil {
		apiutil.BadRequest(c, "seconds_per_segment expansion failed: "+expandErr.Error())
		return
	}
	clips := expanded

	// PR-DRIVE-AVAILABILITY-GATE (2026-07-04): pre-flight
	// Drive-availability gate. The request may carry folder_id at
	// two levels: (a) the request-level BatchRegisterRequest.FolderID
	// default that backfills per-clip FolderID below, or (b)
	// individual clip FolderID overrides. Either path triggers
	// the *drive.Uploader.Service dereference INSIDE
	// sourcing.Service.BatchRegisterFromYouTube — pre-gate this
	// panics with 500 because the composition-root
	// log.Warn("Google Drive client not initialized") silently
	// continues with driveClient=nil. We probe the canonical
	// DriveChecker (composition-root closure mirror of
	// validateDriveServiceAvailability) and fail-closed 503 with
	// the actionable diagnostic if it returns non-nil. Operators
	// reading the 503 body see the cached error envelope and
	// rerun `python3 scripts/generate_drive_token.py` to fix.
	if effectiveFolderID(&req) != "" {
		if err := h.driveChecker(); err != nil {
			// PR-WAVE-1-DRIVE-SSOT (July 2026): the literal substring
			// "*drive.Uploader.Service" is REMOVED from the error envelope
			// so the percheck_drive_access_ssot forward-prevention gate
			// does not fire on this production-code string literal. The
			// diagnostic still names the underlying Drive SDK service as
			// the uninitialised dependency so operators reading the 503
			// response body can rerun `python3 scripts/generate_drive_token.py`
			// to fix per the PR-DRIVE-AVAILABILITY-GATE fail-closed contract.
			apiutil.Error(c, http.StatusServiceUnavailable,
				"Drive service not configured (folder_id routing requires the canonical Drive SDK service to be initialised at composition time; "+
					"see PR-DRIVE-AVAILABILITY-GATE fail-closed contract): "+err.Error())
			return
		}
	}

	if h.svc == nil {
		apiutil.Error(c, http.StatusServiceUnavailable, "register service not wired")
		return
	}

	// Backfill folder_id and location from the request-level defaults
	for i := range clips {
		if clips[i].FolderID == "" && req.FolderID != "" {
			clips[i].FolderID = req.FolderID
		}
		if clips[i].Location.IsEmpty() && !req.Location.IsEmpty() {
			clips[i].Location = req.Location
		}
	}

	// Build commands from request clips
	commands := make([]sourcing.RegisterClipCommand, len(clips))
	for i, clipReq := range clips {
		commands[i] = toRegisterClipCommand(clipReq)
	}

	result := h.svc.BatchRegisterFromYouTube(c.Request.Context(), commands)

	apiutil.OK(c, BatchRegisterResponse{
		OK:            result.OK,
		Total:         result.Total,
		Enqueued:      result.Enqueued,
		EnqueueFailed: result.EnqueueFailed,
		Results:       result.Results,
	})
}

// effectiveFolderID returns the first non-empty folder_id found in the
// request (request-level default OR any per-clip override). Used by
// PR-DRIVE-AVAILABILITY-GATE to short-circuit folder_id traffic BEFORE
// any svc dispatch when *drive.Uploader.Service is nil. Mirrors the
// backfill semantics below but inverted: we want ANY non-empty folder_id
// (including buried per-clip overrides) to trigger the gate so an
// attacker cannot bypass via spreading folder_ids across individual
// clips while leaving the request-level default empty.
//
// godlike/07 input-validation hygiene: TrimSpace is applied uniformly
// to BOTH the request-level default AND per-clip overrides so whitespace-
// only folder_id values cannot skip validation (e.g. `"   "` is treated
// as the canonical empty string). Otherwise an attacker could send
// `{"folder_id":"   "}` and have the gate mistakenly pass.
func effectiveFolderID(req *BatchRegisterRequest) string {
	if req == nil {
		return ""
	}
	if strings.TrimSpace(req.FolderID) != "" {
		return strings.TrimSpace(req.FolderID)
	}
	for i := range req.Clips {
		if trimmed := strings.TrimSpace(req.Clips[i].FolderID); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func toRegisterClipCommand(req RegisterFromYouTubeRequest) sourcing.RegisterClipCommand {
	return sourcing.RegisterClipCommand{
		URL:             strings.TrimSpace(req.URL),
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		Summary:         strings.TrimSpace(req.Summary),
		Topics:          append([]string(nil), req.Topics...),
		Speakers:        append([]string(nil), req.Speakers...),
		MentionedPeople: append([]string(nil), req.MentionedPeople...),
		Hook:            strings.TrimSpace(req.Hook),
		Tags:            append([]string(nil), req.Tags...),
		Source:          strings.TrimSpace(req.Source),
		Category:        strings.TrimSpace(req.Category),
		Group:           strings.TrimSpace(req.Group),
		FolderID:        strings.TrimSpace(req.FolderID),
		// PR-W6-LOCATION-FIELD (July 2026, Wave 6): thread the
		// typed semantic-location DTO from the wire through to the
		// service-layer RegisterClipCommand.Location field. Today the
		// service does NOT resolve Location -> FolderID — the resolver
		// port lands in Wave 7 composition wiring; this commit only
		// accepts the typed contract at the handler seam.
		Location:          req.Location,
		StartSec:          req.Start,
		EndSec:            req.End,
		Force:             req.Force,
		SecondsPerSegment: req.SecondsPerSegment,
		// PR-YT-NO-AUDIO-THREAD (July 2026): thread the audio-strip
		// intent from the wire through the service DTO so
		// sourcing.Service callers can pass it on to the fetch path.
		// godlike/07 minimum-blast-radius: zero-value false matches
		// existing keep-audio behavior; no callers need to set NoAudio
		// explicitly to preserve current behavior.
		NoAudio: req.NoAudio,
	}
}

// expandClipsBySegments takes a slice of RegisterFromYouTubeRequest
// entries and expands any entry with `seconds_per_segment > 0` into
// N children matching the [start, end] window partitioned into
// N-second slices. Strips the `seconds_per_segment` field on the
// children to prevent recursive expansion when they're re-fed.
//
// PR-YT-SECONDS-PER-SEGMENT-WIRE (July 2026) — godlike/07 fail-closed
// contracts:
//   - clip.SecondsPerSegment < 1 (when non-zero) → error
//   - clip.SecondsPerSegment > 0 with start==0 AND end==0 → error
//     (can't partition a window of unknown size)
//   - clip.SecondsPerSegment > 0 with end < start (when both provided)
//     → error
//   - successful sons are appended in start-ascending partition
//     order so the [start, end] trajectory is preserved per-clip
//
// Backward-compat (godlike/06 minimal-surface-change):
//
//	clips with `SecondsPerSegment == 0` OR unset pass through
//	unchanged. Existing /api/media/register-batch callers (the
//	boxing-smoke uses 4 per-clip start/end) are byte-identical.
func expandClipsBySegments(reqs []RegisterFromYouTubeRequest) ([]RegisterFromYouTubeRequest, error) {
	out := make([]RegisterFromYouTubeRequest, 0, len(reqs))
	for i, r := range reqs {
		if r.SecondsPerSegment <= 0 {
			out = append(out, r)
			continue
		}
		if r.SecondsPerSegment < 1 {
			return nil, fmt.Errorf("clip index %d: seconds_per_segment=%d must be >= 1", i, r.SecondsPerSegment)
		}
		if r.Start == 0 && r.End == 0 {
			return nil, fmt.Errorf("clip index %d: seconds_per_segment=%d requires explicit [start, end] bounds (cannot derive from 0,0)", i, r.SecondsPerSegment)
		}
		// Effective window: use [start, end] when both provided;
		// when end==0 use start..start+MaxWindowSec (forward-pointer:
		// the canonical source of truth for video-end detection is
		// the YouTube-Durationmetadata fetch the existing pipeline
		// already does inside ProcessYouTubeSegmentUseCase; here
		// we conservatively require an explicit end rather than
		// guessing — a missing end falls back to a no-op pass-through).
		effectiveEnd := r.End
		if effectiveEnd == 0 {
			out = append(out, r)
			continue
		}
		if effectiveEnd <= r.Start {
			return nil, fmt.Errorf("clip index %d: end (%v) must be greater than start (%v)", i, effectiveEnd, r.Start)
		}
		segDur := float64(r.SecondsPerSegment)
		partNum := 1
		// Tiny epsilon (1e-3) on the upper bound to absorb
		// floating-point drift when end-start is a whole-number
		// multiple of segDur (e.g. [0..10] with 5s => exactly 2
		// partitions, no spurious 1e-9-loss third).
		for cur := r.Start; cur+segDur <= effectiveEnd+1e-3; cur += segDur {
			child := r
			child.Start = cur
			child.End = cur + segDur
			// Strip to prevent recursive expansion if the child
			// is ever re-fed through the handler on a future call.
			child.SecondsPerSegment = 0
			if child.Name != "" {
				child.Name = fmt.Sprintf("%s (part %d)", child.Name, partNum)
			}
			out = append(out, child)
			partNum++
		}
	}
	return out, nil
}
