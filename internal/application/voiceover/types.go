package voiceover

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	promoTypes "github.com/Marcuss-ops/PipelineGen/internal/application/workflow/promo"
	"github.com/Marcuss-ops/PipelineGen/internal/application/translation"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	pathutil "github.com/Marcuss-ops/PipelineGen/pkg/pathutil"
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
		payload["destination"] = map[string]any{
			"group":            r.Destination.Group,
			"folder_id":        r.Destination.FolderID,
			"folder_path":      r.Destination.FolderPath,
			"subfolder_name":   r.Destination.SubfolderName,
			"create_subfolder": r.Destination.CreateSubfolder,
		}
	}
	if len(r.Metadata) > 0 {
		payload["metadata"] = r.Metadata
	}
	return payload
}

type DestinationRequest struct {
	Group           string `json:"group,omitempty"`
	FolderID        string `json:"folder_id,omitempty"`
	FolderPath      string `json:"folder_path,omitempty"`
	SubfolderName   string `json:"subfolder_name,omitempty"`
	CreateSubfolder bool   `json:"create_subfolder,omitempty"`
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
	Group      string
	FolderID   string
	FolderPath string
	DriveLink  string
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
