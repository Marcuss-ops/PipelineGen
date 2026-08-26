// Package register — requests.go (PR-SPLIT-REGISTER-HANDLER, July 2026).
//
// Owns the 3 request/response DTOs (RegisterFromYouTubeRequest +
// BatchRegisterRequest + BatchRegisterResponse). Extracted from
// handler.go per godlike/06 SSOT one-canonical-owner-per-fact: this
// file is the SOLE canonical owner of the wire-shape DTOs (the JSON
// shapes the API exposes/consumes).
//
// Sibling files in the register family (post-split canonical layout):
//   - handler.go (slim orchestrator) — Handler + NewHandler + RegisterRoutes
//   - RegisterFromYouTube + BatchRegisterFromYouTube + effectiveFolderID
//   - toRegisterClipCommand + expandClipsBySegments + the reserved
//     expansion sentinel constant.
//   - requests.go (this file) — the 3 wire-shape DTOs.
//   - module.go (unchanged) — composition-root wiring.
//   - handler_test.go (unchanged) — test surface.
//   - gate_test.go (unchanged) — preflight gate tests.
package register

import (
	asset "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/sourcing"
	domaindelivery "github.com/Marcuss-ops/PipelineGen/internal/kernel/delivery"
)

// RegisterFromYouTubeRequest is the JSON body for registering a clip from a YouTube URL.
//
// PR-RICH-METADATA (July 2026): added Summary, Topics, Speakers, MentionedPeople,
// Hook fields. All are omitempty for backward compatibility — existing callers
// that pack everything into Description continue to work unchanged. The rich
// fields flow through: handler → RegisterClipCommand → job payload →
// ExistingClip → asset.Asset.Metadata → media_assets.metadata_json.
type RegisterFromYouTubeRequest struct {
	URL             string   `json:"url" binding:"required"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Summary         string   `json:"summary,omitempty"`
	Topics          []string `json:"topics,omitempty"`
	Speakers        []string `json:"speakers,omitempty"`
	MentionedPeople []string `json:"mentioned_people,omitempty"`
	Hook            string   `json:"hook,omitempty"`
	Tags            []string `json:"tags"`
	Source          string   `json:"source"`
	Category        string   `json:"category"`
	Group           string   `json:"group"`
	// Location (SEMANTIC-LOCATION-API-2026-07-06 Wave 6) is the
	// canonical semantic-location DTO (godlike/06 SSOT owner:
	// internal/domain/delivery/location.go). When non-empty, the
	// service-layer LocationResolver port (forward-pointer to Wave
	// 7) is intended to resolve this into a concrete FolderID. Today
	// only the typed contract is accepted at the handler seam; the
	// resolver port + composition-root wiring lands in Wave 7. The
	// legacy FolderID field below is DEPRECATED and stays for
	// backward-compat until 2026-12-31 (per FASE 2.1 freeze).
	Location domaindelivery.AssetLocationInput `json:"location,omitempty"`
	FolderID string                            `json:"folder_id"`
	Start    float64                           `json:"start"`
	End      float64                           `json:"end"`
	Force    bool                              `json:"force"`
	// PR-YT-SECONDS-PER-SEGMENT-WIRE (July 2026): when > 0, the
	// handler expands this clip into N children matching
	// [start, end] partitioned into N-second slices. Each child is
	// fed through the canonical per-clip register pipeline so each
	// becomes its own clip_id + Drive upload entry. omitempty keeps
	// existing callers byte-identical (zero-value => no expansion).
	//
	// PR-YT-NO-AUDIO-THREAD (July 2026): when true, the per-segment
	// clip uploaded to Drive has its audio track stripped at FFmpeg
	// (`-an` flag). Default false preserves the existing keep-audio
	// behavior. Combined with SecondsPerSegment, the user can
	// auto-cut a clip into N-second chunks WITHOUT audio via
	// /api/media/register-batch.
	//
	// Fan-out inheritance: each child produced by
	// expandClipsBySegments inherits the parent's NoAudio via struct
	// value copy (`child := r`) byte-stable (godlike/07 Step-2
	// inheritance).
	SecondsPerSegment int `json:"seconds_per_segment,omitempty"`
	// PR-YT-NO-AUDIO-THREAD (July 2026): wire field for the no-audio
	// strip flag. omitempty keeps existing callers byte-identical
	// (false = preserve audio = pre-existing behavior). The single-clip
	// endpoint (/register-from-youtube) inherits the field but its
	// semantics are unchanged: one clip with optional audio strip.
	// Provider-level mapping (req.NoAudio -> ProcessSegmentCommand.KeepAudio
	// = ptr(!NoAudio)) is the canonical translation site; it lives at
	// the YouTube provider's Fetch body (forward-pointer in this commit).
	NoAudio bool `json:"no_audio,omitempty"`
}

// BatchRegisterRequest is the JSON body for batch registering clips from YouTube.
//
// PR-W6-LOCATION-FIELD (July 2026, Wave 6): added Location field
// (semantic-location DTO). Same canonical SSOT contract as the
// single-clip endpoint above. Future Wave 7 ports the
// service-layer LocationResolver so request-level Location
// backfills per-clip empty Locations (mirroring the existing
// FolderID backfill semantics below).
type BatchRegisterRequest struct {
	Location domaindelivery.AssetLocationInput `json:"location,omitempty"`
	FolderID string                            `json:"folder_id"`
	Clips    []RegisterFromYouTubeRequest      `json:"clips" binding:"required"`
}

// BatchRegisterResponse is the response for batch registration.
//
// PR-BATCH-REGISTER-ASYNC (July 2026): Succeeded→Enqueued, Failed→EnqueueFailed.
// Enqueued counts jobs successfully submitted to the worker queue;
// EnqueueFailed counts per-clip enqueue errors. Per-clip BatchClipResult.OK
// is always false (outcome unknown at enqueue time).
type BatchRegisterResponse struct {
	OK            bool                       `json:"ok"`
	Total         int                        `json:"total"`
	Enqueued      int                        `json:"enqueued_count"`
	EnqueueFailed int                        `json:"enqueue_failed"`
	Results       []sourcing.BatchClipResult `json:"results"`
}
