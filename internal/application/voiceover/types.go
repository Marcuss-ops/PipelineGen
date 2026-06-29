package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	promoTypes "github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/application/voiceover/persistence"
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

type BatchRequest struct {
	Text             string              `json:"text"`
	Languages        []string            `json:"languages"`
	FilenameTemplate string              `json:"filename_template"`
	RemoveSilence    *bool               `json:"remove_silence,omitempty"`
	Strategy         string              `json:"strategy"`
	Destination      *DestinationRequest `json:"destination,omitempty"`
	Metadata         map[string]any      `json:"metadata,omitempty"`
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
	ID           string `json:"id"`
	Language     string `json:"language"`
	Voice        string `json:"voice,omitempty"`
	Filename     string `json:"filename"`
	LocalPath    string `json:"local_path,omitempty"`
	CleanedPath  string `json:"cleaned_path,omitempty"`
	DriveLink    string `json:"drive_link,omitempty"`
	DriveFileID  string `json:"drive_file_id,omitempty"`
	DownloadLink string `json:"download_link,omitempty"`
	FileHash     string `json:"file_hash,omitempty"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	SearchText   string `json:"search_text,omitempty"`
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
	Group         string
	FolderID      string
	FolderPath    string
	DriveLink     string
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

func (i *BatchItem) fail(status string, err error) BatchItem {
	i.Status = status
	i.Error = err.Error()
	return *i
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
	return req
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
