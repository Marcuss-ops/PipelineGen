package scriptgeneration

import (
	"bytes"
	"context"
	"fmt"
	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// final_job_image_preflight.go: the pre-enqueue entity-image gate (the r10
// failure class). A plan can promise entity-image overlays whose assets were
// never verified into a content address; the card then fails open to
// text-only and the run renders visibly WITHOUT its photographs — detected
// only after a full remote render. Like enforceFinalJobPreflight for media
// locators, this gate rejects the predictable rejection class locally,
// before a Master attempt exists.
//
// Fail-closed on the TOTAL class only: expected > 0 with zero verified
// assets is the silent-empty-visual incident; it errors. A PARTIAL miss is
// the documented fail-open policy (one invalid binding keeps its card
// text-only rather than failing the whole run) and stays visible through
// the overlay-plan index warn instead.

// finalJobImageVerifiedRef reports whether one asset reference is a
// verified, renderable identity: a 64-hex content address plus a locator
// (Drive/public URL or a usable local path) the worker can actually fetch.
func finalJobImageVerifiedRef(ref capmodel.OverlayAssetRef) bool {
	sha := strings.TrimSpace(ref.SHA256)
	if len(sha) != 64 {
		return false
	}
	for _, r := range sha {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') && (r < 'A' || r > 'F') {
			return false
		}
	}
	// LocalPath is a cache, not the identity: a stale or partially written
	// local copy must not poison a valid content-addressed remote asset. Try
	// the local candidate first, then fall back to the URL whenever its bytes
	// fail the hash/format check.
	if path := strings.TrimSpace(ref.LocalPath); path != "" && usableLocalAssetPath(path) {
		if file, err := os.Open(path); err == nil {
			verified := finalJobImageBytesVerified(file, sha)
			file.Close()
			if verified {
				return true
			}
		}
	}
	if location := strings.TrimSpace(ref.URL); location != "" {
		return finalJobImageURLVerified(location, sha)
	}
	return false
}

func finalJobImageBytesVerified(reader io.Reader, sha string) bool {
	data, err := io.ReadAll(io.LimitReader(reader, 32<<20))
	if err != nil || len(data) == 0 || len(data) >= 32<<20 {
		return false
	}
	if !strings.EqualFold(digest.SHA256Bytes(data), sha) {
		return false
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	return err == nil && config.Width > 0 && config.Height > 0 && (format == "jpeg" || format == "png" || format == "gif")
}

const (
	// finalJobImagePreflightWorkers bounds the preflight fan-out. The gate
	// used to verify every selected image sequentially; a 20-image plan could
	// spend up to 12 minutes in Drive timeouts before failing the whole run
	// (r47). Eight concurrent downloads keep the same verification contract
	// while making the gate a short, bounded step.
	finalJobImagePreflightWorkers = 8
	// finalJobImageFetchTimeout is the per-attempt network bound. It mirrors
	// the historical 12 s client timeout.
	finalJobImageFetchTimeout = 12 * time.Second
	// finalJobImageFetchAttempts covers transient Drive/provider failures:
	// 429/5xx, timeouts, and a 200 response that carries an HTML/error page
	// instead of the image bytes.
	finalJobImageFetchAttempts = 3
)

// finalJobImageURLVerified fetches the remote bytes and verifies the content
// address. The retry wraps the WHOLE fetch+verify, not only the HTTP status:
// a 200 response with an HTML interstitial or a truncated body is exactly the
// r47 failure class, and it must not turn a valid image into a full-run
// preflight rejection on the first attempt.
func finalJobImageURLVerified(location, sha string) bool {
	client := &http.Client{Timeout: finalJobImageFetchTimeout}
	for attempt := 0; attempt < finalJobImageFetchAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
		}
		response, err := client.Get(location)
		if err != nil {
			continue
		}
		verified := false
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			verified = finalJobImageBytesVerified(response.Body, sha)
		}
		response.Body.Close()
		if verified {
			return true
		}
	}
	return false
}

func anyFinalJobImageVerifiedRef(refs []capmodel.OverlayAssetRef) bool {
	for _, ref := range refs {
		if finalJobImageVerifiedRef(ref) {
			return true
		}
	}
	return false
}

// finalJobImageVerifiedItem carries the per-kind admission rule:
//   - a plain entity-image item is verified when it holds at least one
//     verified asset reference;
//   - a composite image group (ImageLayers, two or more entities joined into
//     one card) is verified when the refs provide a verified content address
//     for every layer: one unsupported layer would render a partial card.
//     The composite's structural multiplicity (>=2 layers, >=2 refs) is
//     already the model validator's contract; this checks content identity.
func finalJobImageVerifiedItem(item capmodel.OverlayItem) bool {
	if len(item.ImageLayers) > 0 {
		if len(item.AssetRefs) < len(item.ImageLayers) {
			return false
		}
		verified := 0
		for _, ref := range item.AssetRefs {
			if finalJobImageVerifiedRef(ref) {
				verified++
			}
		}
		return verified >= len(item.ImageLayers)
	}
	return anyFinalJobImageVerifiedRef(item.AssetRefs)
}

// finalJobImageRequested reports whether the payload asked for entity images.
//
// The r20minuti artifact (2026-11-03) enabled images through the extraction
// surface (`include: [persons, ...]`) and `max_image_overlays`, without the
// legacy `entity_images.enabled` toggle. Reading only the toggle returned
// false, the preflight was skipped, and the run shipped a silent image-free
// render after a full remote attempt. EntityImageSurfaceEnabled is the
// contract that already owns this question (see kernel/media plan.go); the
// preflight must ask it instead of re-deriving a narrower condition.
func finalJobImageRequested(req GenerateRequest) bool {
	if !req.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		return false
	}
	// Present 0 disables the group in the payload contract. Temporal absence
	// is not a budget mismatch; the zero value keeps the certified default.
	if limit, ok := req.OverlayGroupLimits[overlayGroupImages]; ok && limit == 0 {
		return false
	}
	// The category is the feature toggle; an explicit images=disabled wins
	// over a stale budget left in the payload.
	toggle, hasCategory := req.OverlayCategories[scriptpkg.OverlayCategoryImages]
	if hasCategory && toggle == scriptpkg.ToggleDisabled {
		return false
	}
	// Any explicit entity-image request is an image contract by itself. Do not
	// make it depend on a second, unrelated overlay budget/category toggle.
	if req.MediaPlan.Extraction.EntityImages.Enabled || req.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return true
	}
	if req.MaxImageOverlays > 0 || req.GenerateSceneImages == scriptpkg.ToggleEnabled {
		return true
	}
	// Zero is the certified default (capabilityoverlay.MaxImageOverlaysPerRun,
	// 20 images), not "off": the overlay canvas already reads it that way. A
	// payload that enables the images category and omits the budget can still
	// plan images, so it must arm the preflight.
	return hasCategory && toggle != scriptpkg.ToggleDisabled
}

// finalJobImagePreflight errors when the plan promises entity-image overlays
// but none carries a verified asset. Items scheduled on intermediate clip
// scenes are excluded through clipScenes: image-style cards there are
// dropped by the final-job handoff by design, so promising them is not a
// broken run.
// finalJobEntityImageGate is the strict counterpart of the opt-in
// VELOX_REQUIRE_ENTITY_IMAGES gate: a final job that requested images must not
// ship text-only cards for some of its selected entities. Partial coverage is
// the r20minuti failure mode that a zero-image check alone cannot see.
//
// The gate must remain opt-in: the default policy accepts the planner's
// text-card fallback for identities without a verified asset. Only an operator
// who explicitly sets VELOX_REQUIRE_ENTITY_IMAGES opts into refusing those
// fallbacks. Without this check the strict gate ran on every final job and
// rejected plans that had already degraded unverified portraits to text.
func finalJobEntityImageGate(audit *OverlayAudit, imageRequested bool) error {
	if !imageRequested {
		return nil
	}
	if audit == nil || audit.EntityImages == nil {
		return fmt.Errorf("final_job preflight: entity images were requested but no entity-image audit was produced")
	}
	info := audit.EntityImages
	if !info.Required {
		return nil
	}
	if info.Selected == 0 {
		return fmt.Errorf("final_job preflight: entity images were requested but the audit selected zero entity-image occurrences; refusing a false 0/0 pass")
	}
	if info.Missing == 0 {
		return nil
	}
	reasons := make([]string, 0, len(info.Reasons))
	for reason, count := range info.Reasons {
		if reason == EntityImageReasonBound || count == 0 {
			continue
		}
		reasons = append(reasons, fmt.Sprintf("%s=%d", reason, count))
	}
	sort.Strings(reasons)
	return fmt.Errorf("final_job preflight: %d of %d selected image entities have no verified asset (%s); refusing text-only cards for requested images", info.Missing, info.Selected, strings.Join(reasons, ", "))
}

func finalJobImagePreflight(items []capmodel.OverlayItem, clipScenes map[string]struct{}, imageRequested bool) error {
	candidates := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		if !strings.Contains(kind, "image") && len(item.ImageLayers) == 0 {
			continue
		}
		if _, dropped := clipScenes[strings.TrimSpace(item.SceneID)]; dropped {
			continue
		}
		candidates = append(candidates, item)
	}
	expected := len(candidates)
	if expected == 0 {
		if imageRequested {
			return fmt.Errorf("final_job preflight: image overlays were enabled and requested, but the compiled plan contains zero verified image overlays; refusing a silent image-free render")
		}
		return nil
	}
	// Verify the selected images concurrently. The callback always returns a
	// nil error: a failed image is data for the aggregate verdict, not a
	// reason to cancel the remaining checks (the error would hide how many
	// assets are actually invalid).
	verified, err := concurrent.Map(context.Background(), candidates, finalJobImagePreflightWorkers,
		func(_ context.Context, _ int, item capmodel.OverlayItem) (bool, error) {
			return finalJobImageVerifiedItem(item), nil
		})
	if err != nil {
		// The callback never returns an error; a recovered panic/abort must
		// fail closed instead of admitting an unvalidated image plan.
		return fmt.Errorf("final_job preflight: image verification aborted: %w", err)
	}
	unverified := 0
	firstUnverified := ""
	for i, ok := range verified {
		if ok {
			continue
		}
		unverified++
		if firstUnverified == "" {
			firstUnverified = strings.TrimSpace(candidates[i].ID)
		}
	}
	// A promised image that is not verified is a silent visual loss whether it
	// is the only one or one of many. Fail closed on ANY unverified image when
	// the job requested images; the old total-only check let a 9/10 coverage
	// pass as if the missing portrait had been rendered.
	if unverified > 0 {
		return fmt.Errorf("final_job preflight: %d of %d selected image overlays failed download/content/hash/image validation; first invalid overlay %q; predicted silent visual loss; refusing to ship unverified promised images", unverified, expected, firstUnverified)
	}
	return nil
}
