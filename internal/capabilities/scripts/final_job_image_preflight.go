package scriptgeneration

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	capmodel "github.com/Marcuss-ops/PipelineGen/internal/capabilities/overlays/model"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
	_ "golang.org/x/image/webp"
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
func finalJobImageVerifiedRefContext(ctx context.Context, ref capmodel.OverlayAssetRef) bool {
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
		return finalJobImageURLVerified(ctx, location, sha)
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
	return err == nil && config.Width > 0 && config.Height > 0 &&
		(format == "jpeg" || format == "png" || format == "gif" || format == "webp")
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

var (
	// finalJobImageAllowLoopback exists only for httptest-backed preflight
	// tests. Production code leaves it false; the package test helper flips it
	// for the duration of a test and restores it in Cleanup.
	finalJobImageAllowLoopback atomic.Bool
	finalJobImageHTTPClient    = newFinalJobImageHTTPClient()
)

// finalJobImageURLVerified fetches the remote bytes and verifies the content
// address. The retry wraps the WHOLE fetch+verify, not only the HTTP status:
// a 200 response with an HTML interstitial or a truncated body is exactly the
// r47 failure class, and it must not turn a valid image into a full-run
// preflight rejection on the first attempt.
func finalJobImageURLVerified(ctx context.Context, location, sha string) bool {
	if err := validateFinalJobImageURL(ctx, location); err != nil {
		return false
	}
	for attempt := 0; attempt < finalJobImageFetchAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
			}
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
		if err != nil {
			return false
		}
		request.Header.Set("User-Agent", "PipelineGen-final-job-image-preflight/1")
		request.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8")
		response, err := finalJobImageHTTPClient.Do(request)
		if err != nil {
			continue
		}
		// 4xx is a verdict, not a blip: retrying 403/404/410 spends the whole
		// fetch budget on an answer that cannot change. 408 and 429 stay
		// retryable because they describe time, not the resource.
		if response.StatusCode >= 400 && response.StatusCode < 500 &&
			response.StatusCode != http.StatusRequestTimeout && response.StatusCode != http.StatusTooManyRequests {
			response.Body.Close()
			return false
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

func newFinalJobImageHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: finalJobImageFetchTimeout, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("final_job preflight: invalid upstream address: %w", err)
		}
		if err := validateFinalJobImageHost(ctx, host); err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			return nil, fmt.Errorf("final_job preflight: resolve upstream %q: %w", host, err)
		}
		for _, resolved := range ips {
			if isFinalJobPrivateIP(resolved.IP) && !(finalJobImageAllowLoopback.Load() && resolved.IP.IsLoopback()) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		}
		return nil, fmt.Errorf("final_job preflight: upstream %q resolved to a blocked address", host)
	}
	client := &http.Client{Transport: transport, Timeout: finalJobImageFetchTimeout}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 {
			return fmt.Errorf("final_job preflight: too many redirects")
		}
		if err := validateFinalJobImageURL(req.Context(), req.URL.String()); err != nil {
			return err
		}
		return validateFinalJobImageHost(req.Context(), req.URL.Hostname())
	}
	return client
}

func validateFinalJobImageURL(ctx context.Context, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || parsed.Hostname() == "" {
		return fmt.Errorf("final_job preflight: invalid image URL")
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
		return fmt.Errorf("final_job preflight: image URL must be absolute HTTP(S)")
	}
	return validateFinalJobImageHost(ctx, parsed.Hostname())
}

func validateFinalJobImageHost(ctx context.Context, host string) error {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || host == "metadata.google.internal" {
		return fmt.Errorf("final_job preflight: blocked image host")
	}
	if config.IsLoopbackHost(host) {
		if finalJobImageAllowLoopback.Load() {
			return nil
		}
		return fmt.Errorf("final_job preflight: blocked loopback image host")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isFinalJobPrivateIP(ip) && !(finalJobImageAllowLoopback.Load() && ip.IsLoopback()) {
			return fmt.Errorf("final_job preflight: blocked private image host")
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("final_job preflight: resolve image host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("final_job preflight: image host resolved to no address")
	}
	for _, resolved := range ips {
		if isFinalJobPrivateIP(resolved.IP) && !(finalJobImageAllowLoopback.Load() && resolved.IP.IsLoopback()) {
			return fmt.Errorf("final_job preflight: image host resolved to a blocked address")
		}
	}
	return nil
}

func isFinalJobPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

func anyFinalJobImageVerifiedRefContext(ctx context.Context, refs []capmodel.OverlayAssetRef) bool {
	for _, ref := range refs {
		if finalJobImageVerifiedRefContext(ctx, ref) {
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
func finalJobImageVerifiedItemContext(ctx context.Context, item capmodel.OverlayItem) bool {
	if len(item.ImageLayers) > 0 {
		if len(item.AssetRefs) < len(item.ImageLayers) {
			return false
		}
		verified := 0
		for _, ref := range item.AssetRefs {
			if finalJobImageVerifiedRefContext(ctx, ref) {
				verified++
			}
		}
		return verified >= len(item.ImageLayers)
	}
	return anyFinalJobImageVerifiedRefContext(ctx, item.AssetRefs)
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
	// Entity-image extraction is an independent surface. It must be checked
	// before the legacy internet-images provider toggle: a payload can request
	// entity cards through the extraction surface while the generic provider
	// policy is absent or disabled, and skipping the preflight there produced
	// the silent image-free render this gate exists to prevent.
	if req.MediaPlan.Extraction.EntityImages.Enabled || req.MediaPlan.Extraction.EntityImageSurfaceEnabled() {
		return true
	}
	if !req.MediaPlan.ProviderPolicy.InternetImages.AsBool() {
		return false
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
	return finalJobImagePreflightContext(context.Background(), items, clipScenes, imageRequested)
}

func finalJobImagePreflightContext(ctx context.Context, items []capmodel.OverlayItem, clipScenes map[string]struct{}, imageRequested bool) error {
	candidates := make([]capmodel.OverlayItem, 0, len(items))
	for _, item := range items {
		kind := strings.ToLower(strings.TrimSpace(item.Kind))
		// An entity card is not required to carry "image" in its kind. Any
		// item with an image binding is part of the image contract and must be
		// validated; only truly image-free items are skipped.
		if !strings.Contains(kind, "image") && len(item.ImageLayers) == 0 && len(item.AssetRefs) == 0 {
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
	verified, err := concurrent.Map(ctx, candidates, finalJobImagePreflightWorkers,
		func(_ context.Context, _ int, item capmodel.OverlayItem) (bool, error) {
			return finalJobImageVerifiedItemContext(ctx, item), nil
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
