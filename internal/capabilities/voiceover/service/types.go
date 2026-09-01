package voiceover

import (
	"fmt"

	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	pathutil "github.com/Marcuss-ops/PipelineGen/internal/platform/filesystem"
)

// Language is the typed BCP-47 envelope for voiceover languages
// (PR-VO-TYPED-PRIMITIVES EXPAND phase, July 2026). Defined as a
// type-alias to string for backward compatibility during the
// EXPAND-phase transition; a future BACKFILL wave will replace the
// alias with a proper `type Language string` value-type carrying
// validation (ParseBCP47, IsValid, Compact form) and typed
// constructors at API boundaries.
//
// Production consumers: treated as typed string for compile-time
// documentation (the alias preserves `language Language` parameter
// readability in stage files). Wire shape: serialises as a plain
// JSON string (the underlying string type).
//
// Language — REMOVED (PR-VO-TYPED-PRIMITIVES, July 2026).
// The canonical typed envelope now lives in language.go as
// `type Language string` (named type, NOT alias). The migration
// from alias to named type closes the audit-flagged primitive-
// obsession on Language across 14+ raw-string sites.

// ── Enums (Status, FailureCode, CompletionState) live in types_enums.go ──

type BatchRequest struct {
	Text string `json:"text"`
	// Languages is the typed BCP-47 list per PR-VO-TYPED-PRIMITIVES
	// (July 2026). JSON wire shape is byte-equivalent with the
	// pre-refactor []string (typed-string slice serialises as the
	// underlying []string). Existing wire consumers see no change.
	Languages        []Language          `json:"languages"`
	FilenameTemplate string              `json:"filename_template"`
	RemoveSilence    *bool               `json:"remove_silence,omitempty"`
	Strategy         string              `json:"strategy"`
	Destination      *DestinationRequest `json:"destination,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
	// Timing is the canonical voiceover timing policy. nil means the
	// pipeline applies the canonical defaults (best_effort / word / [json]).
	Timing *audio.TimingRequest `json:"voiceover_timing,omitempty"`
	// VoiceOverrides is the canonical per-language voice override map
	// keyed by BCP-47 code (mapped to voice identifiers like
	// "it-IT-IsabellaNeural"). nil-safe (synthesizeStage reads this map
	// via voiceOverrideFor() and falls back to "" when missing). The
	// previous pre-P0.4 implementation forwarded the same map via
	// req.Metadata["voice_overrides"] inside ProcessOneVoiceoverUseCase
	// (a metadata-hack envelope) but synthesizeStage never read it;
	// post-P0.4 micro-commit #3 (June 2026) the field is canonical at the
	// struct level.
	VoiceOverrides map[string]string `json:"voice_overrides,omitempty"`

	// RequestID is the stable correlation identifier threaded from the
	// API caller through the parent job and into the child handler. When
	// non-empty, GenerateBatch uses this value instead of generating a
	// new buildRequestID(). Populated by ProcessOneVoiceoverUseCase from
	// the child command's RequestID.
	RequestID string `json:"request_id,omitempty"`
	Project   string `json:"project,omitempty"`
}

func (r *BatchRequest) PayloadMap() map[string]any {
	if r == nil {
		return map[string]any{}
	}

	payload := map[string]any{
		"text":              r.Text,
		"languages":         r.Languages,
		"filename_template": r.FilenameTemplate,
		"strategy":          r.Strategy,
	}
	if len(r.VoiceOverrides) > 0 {
		// PR-VO-AUDIT-P04 micro-commit #3 (June 2026): VoiceOverrides
		// is the canonical per-language voice map and belongs in the
		// top-level payload (NOT inside metadata — the previous
		// metadata-hack envelope collided with the merge-user-metadata
		// collision-drop contract). Round-trips on the consumer side
		// via the BufferRequest.VoiceOverrides field.
		payload["voice_overrides"] = r.VoiceOverrides
	}
	if r.RemoveSilence != nil {
		payload["remove_silence"] = *r.RemoveSilence
	}
	if r.Destination != nil {
		// PR-VO-B2 (June 2026): style_group is serialised into the
		// destination sub-map when non-empty so a worker that re-hydrates
		// the BatchRequest from JSON recovers the full routing intent
		// (Group + FolderID/FolderPath + SubfolderName + StyleGroup).
		// omitempty at the field layer + presence check here keeps the
		// payload identical for legacy callers (no style_group key
		// appears at all when StyleGroup is unset).
		destPayload := map[string]any{
			"group":            r.Destination.Group,
			"folder_id":        r.Destination.FolderID,
			"folder_path":      r.Destination.FolderPath,
			"subfolder_name":   r.Destination.SubfolderName,
			"create_subfolder": r.Destination.CreateSubfolder,
		}
		if r.Destination.Kind != "" {
			destPayload["kind"] = r.Destination.Kind
		}
		if r.Destination.StyleGroup != "" {
			destPayload["style_group"] = string(r.Destination.StyleGroup)
		}
		payload["destination"] = destPayload
	}
	if len(r.Metadata) > 0 {
		payload["metadata"] = r.Metadata
	}
	if r.Timing != nil {
		payload["voiceover_timing"] = r.Timing
	}
	if r.Project != "" {
		payload["project"] = r.Project
	}
	return payload
}

type DestinationRequest struct {
	// Kind (PR-VO-C1, June 2026) is the routing-strategy selector that
	// tells the canonical `/api/voiceover/generate` endpoint how to
	// interpret the rest of the DestinationRequest fields:
	//
	//   - "group"    : MUST use GroupsResolver to map Group → FolderID.
	//                  Empty Group when Kind=="group" is a hard 400.
	//   - "explicit" : MUST use the caller-supplied FolderID verbatim
	//                  (no resolver call). Empty FolderID is a hard 400.
	//   - "" or any other value : legacy auto-detect — if Group is set
	//                  the resolver is consulted; if FolderID is set it
	//                  is used directly; else the config-level voiceover
	//                  folder is the final fallback (if configured).
	Kind string `json:"kind,omitempty"`
	// Group is the human-readable Drive category name (e.g. "boxe").
	Group string `json:"group,omitempty"`
	// FolderID is the explicit Drive folder ID. Empty means "resolve from
	// Group / SubfolderName via the canonical GroupsResolver tree".
	FolderID string `json:"folder_id,omitempty"`
	// FolderPath is the readable path mirror (informational; resolver
	// is the authority for the actual folder structure).
	FolderPath string `json:"folder_path,omitempty"`
	// SubfolderName is the optional subfolder to create/use. Validated
	// by DestinationRequest.Validate (PR-VO-A4) so a path-traversal
	// payload cannot escape.
	SubfolderName string `json:"subfolder_name,omitempty"`
	// CreateSubfolder controls whether the resolver creates a new
	// subfolder when the name doesn't yet exist.
	CreateSubfolder bool `json:"create_subfolder,omitempty"`
	// StyleGroup (PR-VO-B2, June 2026) is the canonical selector that
	// buckets voiceovers into a "style cohort" — a coarse-grained
	// routing tag alongside Group (specific folder) and SubfolderName
	// (leaf subfolder). Surfaced in the per-asset metadata.json
	// manifest under the key `style_group` so downstream consumers
	// (Qdrant re-rankers, style-cohort analytics, audit replay) can
	// recover the original selection verbatim. omitempty so legacy
	// callers (no field set) don't carry a bogus empty key.
	//
	// Typed (StyleGroup) per PR-VO-TYPED-PRIMITIVES — JSON wire
	// shape is byte-equivalent with the pre-refactor string field.
	StyleGroup StyleGroup `json:"style_group,omitempty"`
	Project    string     `json:"project,omitempty"`
}

// Validate runs the security-relevant bounds-checks on a request that
// has been bound from JSON or constructed programmatically. Returns nil
// when every slot is safe to consume downstream. Each check is
// deliberately a no-op (returns nil) when the slot is empty so absence
// stays a legitimate signal (e.g. SubfolderName empty == "do not
// create subfolder").
//
// PR-VO-A4 (path-traversal fix, June 2026): the previous
// DestinationRequest was unvalidated at the type layer, so any caller
// that built it directly (the handler binding layer, the
// jobs/scripts subsystem, the YouTube adapters) had to re-implement
// the same checks per call site. The canonical Validate now lives
// here so:
//
//   - handler binding can call d.Validate() right after c.ShouldBindJSON
//     and return 400 instead of continuing into GenerateBatch;
//   - service.GenerateBatch can call d.Validate() once at the boundary
//     and fail-fast for the whole batch (rather than per-language);
//   - voiceover.processLanguage's MkdirAll site is the last line of
//     defense, calling SanitizeSubfolderSegment + EnsureWithinDir, so
//     direct callers that bypass GenerateBatch still cannot pass
//     path-traversal payloads to os.MkdirAll.
//
// The error returned wraps the canonical pkg/pathutil surface without
// adding a duplicate prefix at this layer (callers wrap further upstream
// if they want context-specific wording — e.g. voiceover.processLanguage
// wraps as "path traversal rejected: %w" for operator audit).
func (d *DestinationRequest) Validate() error {
	if d == nil {
		return nil
	}
	// PR-VO-C1 invariants (godlike/07 no-fake-availability):
	//   - kind="group" + empty group     → hard error (GroupsResolver would 404)
	//   - kind="explicit" + empty folder_id → hard error (would silently fall back)
	if d.Kind == "group" && d.Group == "" {
		return fmt.Errorf("kind=\"group\" requires non-empty group")
	}
	if d.Kind == "explicit" && d.FolderID == "" {
		return fmt.Errorf("kind=\"explicit\" requires non-empty folder_id")
	}
	if d.SubfolderName == "" {
		return nil
	}
	if _, err := pathutil.SanitizeSubfolderSegment(d.SubfolderName); err != nil {
		return fmt.Errorf("subfolder_name: %w", err)
	}
	return nil
}

type BatchResponse struct {
	OK        bool        `json:"ok"`
	RequestID string      `json:"request_id"`
	Items     []BatchItem `json:"items"`
	Error     string      `json:"error,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"`
}

type BatchItem struct {
	ID            string `json:"id"`
	Voice         string `json:"voice,omitempty"`
	Filename      string `json:"filename"`
	LocalPath     string `json:"local_path,omitempty"`
	CleanedPath   string `json:"cleaned_path,omitempty"`
	DriveLink     string `json:"drive_link,omitempty"`
	DriveFileID   string `json:"drive_file_id,omitempty"`
	DownloadLink  string `json:"download_link,omitempty"`
	LegacyFileMD5 string `json:"legacy_file_md5,omitempty"`
	// Language is the typed BCP-47 envelope (voiceover.Language)
	// per PR-VO-TYPED-PRIMITIVES (July 2026) — JSON wire shape is
	// byte-equivalent with the pre-refactor string field.
	Language Language `json:"language"`
	Status   Status   `json:"status"`
	// PR-VO-AUDIT-P01 (June 2026): the legacy checks
	// `if strings.TrimSpace(item.Status) == "failed"` (process.go)
	// + `if item.Status == "failed"` (stages.go) silently missed
	// every "*/_failed" legacy literal — tts_failed, upload_failed,
	// missing_folder_id, no_local_payload, lifecycle_unavailable,
	// db_*, tx_*, outbox_*. After the audit fix, the canonical
	// fail() helper ALWAYS normalises any failure code to Status=StatusFailed
	// + appends the specific FailureCode to item.Errors so the
	// forensic trail is preserved without affecting the aggregate
	// OK=false contract (ok = ok && item.Status == StatusCompleted && item.Error == "").
	Error string `json:"error,omitempty"`
	// ErrorCode is the stable machine-readable failure code propagated
	// from the per-item pipeline. In particular, destination integrity
	// failures must remain visible on the batch/API result and not only
	// in the child-job envelope.
	ErrorCode string `json:"error_code,omitempty"`

	// Errors is the structured failure history. fail() appends the
	// call's FailureCode so callers can correlate the canonical
	// StatusFailed with the specific failure mode. omitempty so
	// happy-path JSON is byte-equivalent to the pre-P01 wire shape.
	Errors     []FailureCode `json:"errors,omitempty"`
	SearchText string        `json:"search_text,omitempty"`
}

type VoiceoverResult struct {
	OK          bool   `json:"ok"`
	Voice       string `json:"voice,omitempty"`
	Path        string `json:"path,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	Error       string `json:"error,omitempty"`
}

type ResolvedDestination struct {
	Group      string
	FolderID   string
	FolderPath string
	DriveLink  string
	// SubfolderName is the optional subfolder to create/use within
	// the resolved folder. Used by the Drive uploader to create a
	// per-script subfolder and upload the voiceover into it.
	SubfolderName string
	// StyleGroup (PR-VO-B2, June 2026) carries the StyleGroup from
	// the originating DestinationRequest through the resolver without
	// round-tripping through ResolveResult. The resolver is a folder
	// mapping layer, not a style-routing layer, so we set it from the
	// caller-supplied destination after the resolver returns — see
	// resolveDestination in process.go.
	//
	// Typed (StyleGroup) per PR-VO-TYPED-PRIMITIVES.
	StyleGroup StyleGroup
}

// PR-VO-AUDIT-P01 (June 2026): the canonical fail() helper. The
// previous implementation forward-stored the literal status (e.g.
// "tts_failed", "upload_failed", "missing_folder_id") and relied on
// downstream checks `if item.Status == "failed"` to gate the
// pipeline. Those checks missed every failure literal that wasn't
// exactly "failed", so a tts_failed in synthesizeStage could fall
// through Stage 2 with `Status == "tts_failed"`, then finalizeStage
// committed the record with `Status = "completed"` — silent false-
// success (the canonical audit P0.1 bug).
//
// The fix normalises every failure to Status(StatusFailed)
// regardless of the specific code, then records the code in
// item.Errors (omitempty-driven) so the forensic trail survives.
// The downstream aggregate check at stages.go::GenerateBatch
// becomes `if item.Status == StatusFailed { ok = false }` and
// process.go's check short-circuits on the same typed comparison —
// no more `strings.TrimSpace ==  "failed"` substring matching.
//
// Nil err case: the helper tolerates nil err (Error stays empty) so
// callers that want to mark a StatusFailed without a concrete
// message (e.g. cancel race) can still surface the failure code in
// Errors[] without lying about an empty message string.
// ── Helper functions (fail, isSuccessful, normalizeBatchRequest, etc.) live in types_helpers.go ──
