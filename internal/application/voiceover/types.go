package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
	promoTypes "github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	pathutil "github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
)

// P1-2 back-compat aliases (June 2026): the canonical wire-shape
// types moved into internal/application/voiceover/persistence/
// (Repository interface, VoiceoverRecord struct). Type aliases
// below keep all pre-P1-2 call sites that imported these names
// from the voiceover root package compilable without churn.
//
// Migration: new code should reference `persistence.Repository`
// and `persistence.VoiceoverRecord` directly. The aliases below
// remain only until the next Wave 21 BACKFILL step (CUTOVER)
// drops them along with the rest of the B-2 typed-port aliases.
type (
	// VoiceoverRepository is the legacy export name; the canonical
	// type lives in the persistence sub-package.
	//
	// Deprecated: use persistence.Repository directly. The alias
	// remains for pre-P1-2 import compatibility; future Wave 21
	// CUTOVER will drop it.
	VoiceoverRepository = persistence.Repository
	// VoiceoverRecord is the legacy export name; the canonical
	// type lives in the persistence sub-package.
	//
	// Deprecated: use persistence.VoiceoverRecord directly. The
	// alias remains for pre-P1-2 import compatibility.
	VoiceoverRecord = persistence.VoiceoverRecord
)

// ────────────────────────────────────────────────────────────────────────
// PR-VO-AUDIT-P01 (June 2026): typed state-machine for voiceover.
//
// REPLACES the legacy string-literal status + ad-hoc failure codes with
// two distinct types (Status / FailureCode). Both underlying strings
// remain identical to the legacy wire shape so JSON consumers see no
// change. The compile-time typed comparison is the whole point: legacy
// checks `if item.Status == "failed"` silently missed every failure
// literal that wasn't exactly "failed" (tts_failed, upload_failed,
// missing_folder_id, etc.), allowing a TTS-failed item to reach the
// finalizeStage and commit a record with Status="completed".
//
// After the typed-enum refactor, ANY failure code flows through the
// canonical fail() helper which normalises to Status=StatusFailed;
// downstream aggregate check `if item.Status == StatusFailed` is
// exhaustive by construction — no substring matching, no "*/_failed"
// gap, no silent false-success.
//
// JSON wire compat: `type Status string` + `type FailureCode string`
// both serialise into the same byte-for-byte strings as the pre-P01
// literal fields. omitempty on Errors[] keeps happy-path rows compact.
// ────────────────────────────────────────────────────────────────────────

// Status is the per-item terminal/active state. Typed so the runtime
// aggregate check (Status == StatusFailed) is exhaustive at compile time
// and cannot silently miss any "*_failed" sub-state.
type Status string

const (
	// StatusProcessing is the initial state set by process.go right
	// after ID + filename build. Visible to API consumers while a
	// pipeline is in-flight; finalizeStage closes with StatusCompleted
	// before commit.
	StatusProcessing Status = "processing"
	// StatusGenerated is the post-synthesize state. Stage 1 success.
	StatusGenerated Status = "generated"
	// StatusUploaded is the post-Lifecycle.ProcessAsset state.
	// Stage 2 success — Drive link + file ID populated.
	StatusUploaded Status = "uploaded"
	// StatusCompleted is the post-finalize state. Stage 3 success —
	// commit succeeded, row in SQLite + index event enqueued in
	// the outbox (atomic, single tx).
	StatusCompleted Status = "completed"
	// StatusFailed is the canonical aggregate failure state. ALL
	// failure codes (FailureCode consts below) normalise to this
	// Status via the BatchItem.fail helper, which preserves the
	// specific FailureCode in item.Errors so the forensic trail
	// survives without breaking the aggregate OK=false contract.
	StatusFailed Status = "failed"
)

// FailureCode is the structured per-failure-mode code. fail() appends
// the call's FailureCode to item.Errors so callers can correlate the
// canonical StatusFailed with the specific failure mode. Each constant
// maps 1-a-1 to the pre-P01 literal status string; the refactor replaces
// the literal with the typed constant without disturbing the JSON wire
// shape.
type FailureCode string

const (
	// FailureTTSProviderUnavailable — synthesizeStage: ttsProvider
	// is nil (composition root did not wire it).
	FailureTTSProviderUnavailable FailureCode = "tts_provider_unavailable"
	// FailureTTS — synthesizeStage: TTSProvider.Synthesize returned
	// an error (Python crash, edge-tts bridge failure, FFmpeg
	// post-process error, etc.).
	FailureTTS FailureCode = "tts_failed"
	// FailureLifecycleUnavailable — destinationStage: lifecycleService
	// is nil (composition root did not wire it).
	FailureLifecycleUnavailable FailureCode = "lifecycle_unavailable"
	// FailureMissingFolder — destinationStage: destination.FolderID
	// is empty (resolver short-circuit; canonical destination
	// resolver surfaces ErrMissingFolder at the resolve step too).
	FailureMissingFolder FailureCode = "missing_folder_id"
	// FailureNoLocalPayload — destinationStage: synthesizeStage
	// produced no local path (Stage 2 cannot upload).
	FailureNoLocalPayload FailureCode = "no_local_payload"
	// FailureUpload — destinationStage: lifecycleService.ProcessAsset
	// returned an error (Drive upload failed, dedupe gate hard-
	// rejected, etc.).
	FailureUpload FailureCode = "upload_failed"
	// FailureDBUnavailable — finalizeStage: voiceoverRepo is nil
	// (composition root did not wire it).
	FailureDBUnavailable FailureCode = "db_unavailable"
	// FailureTxBegin — finalizeStage: voiceoverRepo.BeginTx returned
	// an error (sqlite lock, schema drift, etc.).
	FailureTxBegin FailureCode = "tx_begin_failed"
	// FailureDBDelete — finalizeStage: DeleteByIDTx returned an
	// error (swap-mode preconditions).
	FailureDBDelete FailureCode = "db_delete_failed"
	// FailureDBInsert — finalizeStage: InsertTx returned an error.
	FailureDBInsert FailureCode = "db_insert_failed"
	// FailureOutboxEnqueue — finalizeStage:
	// outboxEnqueuer.EnqueueIndexEvent returned an error (indexing
	// deferred; row already in tx).
	FailureOutboxEnqueue FailureCode = "outbox_enqueue_failed"
	// FailureTxCommit — finalizeStage: tx.Commit returned an error.
	FailureTxCommit FailureCode = "tx_commit_failed"
	// FailureInvalidSubfolder — processLanguage: SubfolderName path
	// traversal rejected by pathutil.SanitizeSubfolderSegment.
	FailureInvalidSubfolder FailureCode = "invalid_subfolder_name"
	// FailureInvalidFilename — processLanguage: SanitizeFilename
	// rejected the caller-supplied filename.
	FailureInvalidFilename FailureCode = "invalid_filename"
	// FailureDownload — preserved for back-compat with the legacy
	// service_test.go fixture at line 236.
	FailureDownload FailureCode = "download_failed"
)

type BatchRequest struct {
	Text             string              `json:"text"`
	Languages        []string            `json:"languages"`
	FilenameTemplate string              `json:"filename_template"`
	RemoveSilence    *bool               `json:"remove_silence,omitempty"`
	Strategy         string              `json:"strategy"`
	Destination      *DestinationRequest `json:"destination,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
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
		if r.Destination.StyleGroup != "" {
			destPayload["style_group"] = r.Destination.StyleGroup
		}
		payload["destination"] = destPayload
	}
	if len(r.Metadata) > 0 {
		payload["metadata"] = r.Metadata
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
	StyleGroup string `json:"style_group,omitempty"`
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
}

type BatchItem struct {
	ID           string   `json:"id"`
	Language     string   `json:"language"`
	Voice        string   `json:"voice,omitempty"`
	Filename     string   `json:"filename"`
	LocalPath    string   `json:"local_path,omitempty"`
	CleanedPath  string   `json:"cleaned_path,omitempty"`
	DriveLink    string   `json:"drive_link,omitempty"`
	DriveFileID  string   `json:"drive_file_id,omitempty"`
	DownloadLink string   `json:"download_link,omitempty"`
	FileHash     string   `json:"file_hash,omitempty"`
	// Status is the canonical per-item terminal/active state. Typed
	// (Status) so the runtime aggregate check (Status == StatusFailed)
	// is exhaustively typed at compile time. The JSON wire shape
	// serialises the underlying string ("processing"/"generated"/
	// "uploaded"/"completed"/"failed") so API consumers see no change.
	//
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
	Status       Status     `json:"status"`
	Error        string     `json:"error,omitempty"`
	// Errors is the structured failure history. fail() appends the
	// call's FailureCode so callers can correlate the canonical
	// StatusFailed with the specific failure mode. omitempty so
	// happy-path JSON is byte-equivalent to the pre-P01 wire shape.
	Errors       []FailureCode `json:"errors,omitempty"`
	SearchText   string        `json:"search_text,omitempty"`
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
	StyleGroup string
}

// Promo types moved to workflow/promo (PR 6, June 2026).
// Type aliases preserve backward compatibility.

// PromoRequest is the promo workflow request.
// Deprecated: use promo.Request directly.
type PromoRequest = promoTypes.Request

// DefaultPromoLanguages returns the 13 promo voiceover languages.
// Deprecated: use translation.DefaultPromoLanguages directly.
var DefaultPromoLanguages = translation.DefaultPromoLanguages

// LanguageTarget pairs a BCP-47 language code with a human-readable name.
// Deprecated: use translation.LanguageTarget directly.
type LanguageTarget = translation.LanguageTarget

// PromoResult holds the result of a single promo voiceover generation.
// Deprecated: use promo.Result directly.
type PromoResult = promoTypes.Result

// PromoResponse aggregates all promo voiceover results.
// Deprecated: use promo.Response directly.
type PromoResponse = promoTypes.Response

// PromoRequestPayloadMap serialises a PromoRequest into a map for the job system.
// PromoRequest is a type alias (workflow/promo.Request) so methods cannot be
// defined on it — use this standalone function instead.
//
// PR-VO-A6 (strict translator failure, June 2026): the payload now
// serialises AllowUntranslated so the async /promo path (where the
// handler enqueues a voiceover.promo job and the worker re-runs
// Generator.Generate via job_handler.go) preserves the original
// strict/lenient intent across the job boundary. JSON name is
// `allow_untranslated` (snake_case) to match the existing payload
// shape; the field is omitempty so legacy callers (no flag set)
// default to strict / fail-closed.
func PromoRequestPayloadMap(r *PromoRequest) map[string]any {
	if r == nil {
		return map[string]any{}
	}
	payload := map[string]any{
		"text":            r.Text,
		"drive_folder_id": r.DriveFolderID,
		"dry_run":         r.DryRun,
	}
	if len(r.Languages) > 0 {
		payload["languages"] = r.Languages
	}
	if r.AllowUntranslated {
		// omitempty semantics: omit when false to preserve payload
		// readability + keep pre-PR-VO-A6 jobs valid (the field is
		// optional from the consumer's perspective).
		payload["allow_untranslated"] = r.AllowUntranslated
	}
	return payload
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
func (i *BatchItem) fail(code FailureCode, err error) BatchItem {
	i.Status = Status(StatusFailed)
	if err != nil {
		i.Error = err.Error()
	}
	i.Errors = append(i.Errors, code)
	return *i
}

func (i BatchItem) isSuccessful() bool {
	return strings.TrimSpace(string(i.Status)) == "completed" && strings.TrimSpace(i.Error) == ""
}

func normalizeBatchRequest(req *BatchRequest) *BatchRequest {
	if req.FilenameTemplate == "" {
		req.FilenameTemplate = "{slug}_{lang}.mp3"
	}
	// PR-VO-A2: route through the canonical asset.PipelineStrategy normaliser so
	// process.go's `req.Strategy == "replace"` branch matches the single source of
	// truth for the three production strategies (verify / skip / replace). Unknown
	// inputs collapse to "verify" — the read-through-cache default — which is the
	// historically documented "no force" behaviour of NormalizeStrategy.
	req.Strategy = string(asset.NormalizeStrategy(req.Strategy, false))
	if len(req.Languages) == 0 {
		req.Languages = []string{"en"}
	}
	if len(req.VoiceOverrides) == 0 {
		if hydrated := voiceOverridesFromMetadata(req.Metadata); len(hydrated) > 0 {
			req.VoiceOverrides = hydrated
		}
	}
	return req
}

func voiceOverridesFromMetadata(metadata map[string]any) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	raw, ok := metadata["voice_overrides"]
	if !ok || raw == nil {
		return nil
	}
	out := map[string]string{}
	switch typed := raw.(type) {
	case map[string]string:
		for lang, voice := range typed {
			if lang == "" || voice == "" {
				continue
			}
			out[lang] = voice
		}
	case map[string]any:
		for lang, value := range typed {
			voice, ok := value.(string)
			if !ok || lang == "" || voice == "" {
				continue
			}
			out[lang] = voice
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildRequestID() string {
	return "vo_" + time.Now().Format("20060102_150405") + "_" + randomSuffix(6)
}

func randomSuffix(n int) string {
	if n <= 0 {
		return ""
	}
	size := (n + 1) / 2
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return time.Now().Format("150405")
	}
	return hex.EncodeToString(buf)[:n]
}
