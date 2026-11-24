package vidrush

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

const vidRushImageUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

var (
	ErrVidRushImageTooLarge = errors.New("vidrush image exceeds download limit")
	ErrVidRushImageInvalid  = errors.New("vidrush image is not a valid supported image")
	// ErrVidRushImageCollage marks a contact sheet, montage or triptych. It
	// wraps ErrVidRushImageInvalid so every soft-reject path keeps working,
	// while the reason stays precise: one "photo" that shows several pictures
	// reads to the viewer as stacked images, so the candidate must not bind.
	ErrVidRushImageCollage = fmt.Errorf("%w: panel grid collage", ErrVidRushImageInvalid)
	ErrVidRushSSRFBlocked  = errors.New("vidrush remote image URL blocked by SSRF policy")
	vidRushImageTransport  = newVidRushImageTransport()
)

func newVidRushImageTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 90 * time.Second
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("vidrush image: invalid upstream address: %w", err)
		}
		if err := validateVidRushResolvedHost(ctx, host); err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(ips) == 0 {
			if err == nil {
				err = ErrVidRushSSRFBlocked
			}
			return nil, fmt.Errorf("vidrush image: resolve upstream: %w", err)
		}
		for _, resolved := range ips {
			if isVidRushPrivateIP(resolved.IP) {
				continue
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		}
		return nil, ErrVidRushSSRFBlocked
	}
	return transport
}

// VidRushImagePolicy is deliberately conservative and CPU-friendly. The
// policy is shared by web-image and generated-image verification.
type VidRushImagePolicy struct {
	MaxBytes int64
	// MinWidth/MinHeight remain the canonical area floor (both were the
	// historical two-sided minimum). MinLongSide/MinShortSide add the
	// orientation-aware rule that admits vertical portraits instead of
	// rejecting a 600x900 image for being narrower than 640.
	MinWidth       int
	MinHeight      int
	MinLongSide    int
	MinShortSide   int
	MaxAspectRatio float64
	// MaxPixels rejects a declared decompression bomb before a full decode.
	MaxPixels    int
	Timeout      time.Duration
	MaxRedirects int
}

func DefaultVidRushImagePolicy() VidRushImagePolicy {
	return VidRushImagePolicy{
		MaxBytes:       20 << 20,
		MinWidth:       640,
		MinHeight:      360,
		MinLongSide:    640,
		MinShortSide:   360,
		MaxAspectRatio: 3.0,
		MaxPixels:      digest.DefaultImagePixelBudget,
		Timeout:        15 * time.Second,
		MaxRedirects:   3,
	}
}

func normalizeVidRushImagePolicy(policy VidRushImagePolicy) VidRushImagePolicy {
	defaults := DefaultVidRushImagePolicy()
	if policy.MaxBytes <= 0 {
		policy.MaxBytes = defaults.MaxBytes
	}
	if policy.MinWidth <= 0 {
		policy.MinWidth = defaults.MinWidth
	}
	if policy.MinHeight <= 0 {
		policy.MinHeight = defaults.MinHeight
	}
	if policy.MinLongSide <= 0 {
		policy.MinLongSide = defaults.MinLongSide
	}
	if policy.MinShortSide <= 0 {
		policy.MinShortSide = defaults.MinShortSide
	}
	if policy.MaxAspectRatio <= 0 {
		policy.MaxAspectRatio = defaults.MaxAspectRatio
	}
	if policy.MaxPixels <= 0 {
		policy.MaxPixels = defaults.MaxPixels
	}
	if policy.Timeout <= 0 {
		policy.Timeout = defaults.Timeout
	}
	if policy.MaxRedirects < 0 {
		policy.MaxRedirects = defaults.MaxRedirects
	}
	return policy
}

// WithinPixelBudget reports whether the declared dimensions fit the decode
// budget. It is a header check only; it never allocates the pixel buffer.
func (p VidRushImagePolicy) WithinPixelBudget(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	policy := normalizeVidRushImagePolicy(p)
	return int64(width)*int64(height) <= int64(policy.MaxPixels)
}

// AcceptsDimensions applies the orientation-aware minimum: long side, short
// side, area and aspect ratio. A portrait 600x900 passes; a 1200x300 banner
// and a tiny 320x240 thumbnail fail. It does not apply the upper pixel bound;
// callers check WithinPixelBudget separately so a bomb maps to "too large"
// rather than "invalid".
func (p VidRushImagePolicy) AcceptsDimensions(width, height int) bool {
	if width <= 0 || height <= 0 {
		return false
	}
	policy := normalizeVidRushImagePolicy(p)
	longSide, shortSide := width, height
	if shortSide > longSide {
		longSide, shortSide = shortSide, longSide
	}
	if longSide < policy.MinLongSide || shortSide < policy.MinShortSide {
		return false
	}
	if policy.MinWidth > 0 && policy.MinHeight > 0 &&
		int64(width)*int64(height) < int64(policy.MinWidth)*int64(policy.MinHeight) {
		return false
	}
	if policy.MaxAspectRatio > 0 && float64(longSide)/float64(shortSide) > policy.MaxAspectRatio {
		return false
	}
	return true
}

// ValidateVidRushRemoteImageURL rejects non-web schemes, credentials,
// loopback and literal private/link-local addresses before any request is
// made. The HTTP client below repeats this check after every redirect.
func ValidateVidRushRemoteImageURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || u.Hostname() == "" {
		return ErrVidRushSSRFBlocked
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.User != nil {
		return ErrVidRushSSRFBlocked
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if config.IsLoopbackHost(host) || host == "metadata.google.internal" {
		return ErrVidRushSSRFBlocked
	}
	if ip := net.ParseIP(host); ip != nil && isVidRushPrivateIP(ip) {
		return ErrVidRushSSRFBlocked
	}
	return nil
}

func isVidRushPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// DownloadVidRushImageForCandidate carries the result page as a referrer when
// the image host requires a normal browser-like navigation context. The image
// URL itself still goes through the same SSRF, size, MIME and redirect guards.
// ProviderImageHTTPClient is the bounded client for provider image downloads.
// It exists so call sites stop passing http.DefaultClient — which has no
// timeout at all — for a fetch that can hang on a slow origin.
var providerImageHTTPClient = &http.Client{
	Timeout: 60 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}

// ProviderImageHTTPClient returns the shared bounded client for image fetches.
func ProviderImageHTTPClient() *http.Client { return providerImageHTTPClient }

func DownloadVidRushImageForCandidate(ctx context.Context, client *http.Client, candidate scriptpkg.SegmentAssetCandidate, policy VidRushImagePolicy) ([]byte, string, error) {
	rawURL := strings.TrimSpace(candidate.SourceURL)
	if rawURL == "" {
		rawURL = strings.TrimSpace(candidate.PreviewURL)
	}
	return downloadVidRushImage(ctx, client, rawURL, candidate.SourcePageURL, policy)
}

func downloadVidRushImage(ctx context.Context, client *http.Client, rawURL, sourcePageURL string, policy VidRushImagePolicy) ([]byte, string, error) {
	if err := ValidateVidRushRemoteImageURL(rawURL); err != nil {
		return nil, "", err
	}
	if err := validateVidRushResolvedHost(ctx, rawURL); err != nil {
		return nil, "", err
	}
	policy = normalizeVidRushImagePolicy(policy)
	if client == nil {
		client = &http.Client{}
	}
	copyClient := *client
	copyClient.Timeout = policy.Timeout
	// Reuse a shared transport so repeated image hosts benefit from pooled
	// connections. Its dialer validates the resolved address at connect time;
	// caller transports and environment proxies cannot bypass the SSRF policy.
	copyClient.Transport = vidRushImageTransport
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > policy.MaxRedirects {
			return fmt.Errorf("vidrush image: maximum redirects exceeded")
		}
		if err := ValidateVidRushRemoteImageURL(req.URL.String()); err != nil {
			return err
		}
		return validateVidRushResolvedHost(req.Context(), req.URL.String())
	}
	// A bounded GET performs the authoritative validation in one request.
	// HEAD was only advisory, added latency and often disagreed with the bytes
	// actually served by image CDNs.
	request, err := newVidRushImageRequest(ctx, rawURL, sourcePageURL)
	if err != nil {
		return nil, "", err
	}
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, "", fmt.Errorf("vidrush image: upstream status %d", response.StatusCode)
	}
	if response.ContentLength > policy.MaxBytes {
		return nil, "", ErrVidRushImageTooLarge
	}
	limited := io.LimitReader(response.Body, policy.MaxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(data)) > policy.MaxBytes {
		return nil, "", ErrVidRushImageTooLarge
	}
	mime := http.DetectContentType(data)
	if !allowedVidRushImageMIME(mime) {
		return nil, mime, ErrVidRushImageInvalid
	}
	declared := strings.TrimSpace(strings.Split(response.Header.Get("Content-Type"), ";")[0])
	if declared != "" && declared != "application/octet-stream" && !strings.EqualFold(declared, mime) {
		return nil, declared, ErrVidRushImageInvalid
	}
	return data, mime, nil
}

func validateVidRushImageHead(status int, _ string, contentLength int64, policy VidRushImagePolicy) error {
	policy = normalizeVidRushImagePolicy(policy)
	if status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented || status == http.StatusForbidden {
		// Some image CDNs deny HEAD while serving the same URL to a normal GET.
		// Treat the preflight as advisory; the guarded GET validates status, MIME,
		// byte limits, and decoded image content before accepting anything.
		return nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return fmt.Errorf("vidrush image: upstream HEAD status %d", status)
	}
	// HEAD metadata is advisory: several image hosts return text/plain or a
	// placeholder length here even though GET serves the actual image. GET
	// validates the response MIME against sniffed bytes and decodes the image.
	if contentLength > policy.MaxBytes {
		return ErrVidRushImageTooLarge
	}
	return nil
}

func newVidRushImageRequest(ctx context.Context, rawURL, sourcePageURL string) (*http.Request, error) {
	return newVidRushImageRequestMethod(ctx, http.MethodGet, rawURL, sourcePageURL)
}

func newVidRushImageRequestMethod(ctx context.Context, method, rawURL, sourcePageURL string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", vidRushImageUserAgent)
	// Advertise only formats the verifier can decode. CDNs honour AVIF in
	// Accept and serve it for a .jpg URL; the Go decoder cannot verify AVIF,
	// so requesting it turns valid candidates into ErrVidRushImageInvalid.
	request.Header.Set("Accept", "image/webp,image/png,image/jpeg,image/gif")
	request.Header.Set("Accept-Language", "en-US,en;q=0.8")
	if strings.TrimSpace(sourcePageURL) != "" && ValidateVidRushRemoteImageURL(sourcePageURL) == nil {
		request.Header.Set("Referer", sourcePageURL)
	}
	return request, nil
}

func validateVidRushResolvedHost(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return ErrVidRushSSRFBlocked
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if !strings.Contains(raw, "://") {
		host = strings.TrimSuffix(strings.ToLower(raw), ".")
	}
	if ip := net.ParseIP(host); ip != nil {
		if isVidRushPrivateIP(ip) {
			return ErrVidRushSSRFBlocked
		}
		return nil
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("vidrush image: resolve host: %w", err)
	}
	if len(addresses) == 0 {
		return ErrVidRushSSRFBlocked
	}
	for _, address := range addresses {
		if isVidRushPrivateIP(address.IP) {
			return ErrVidRushSSRFBlocked
		}
	}
	return nil
}

// VerifyVidRushImageBytes performs the deterministic technical checks after
// acquisition. It does not assert rights: rights are a separate policy input.
func VerifyVidRushImageBytes(candidate scriptpkg.SegmentAssetCandidate, data []byte, mime string, policy VidRushImagePolicy) (scriptports.VerifiedArtifact, error) {
	policy = normalizeVidRushImagePolicy(policy)
	if len(data) == 0 {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	if int64(len(data)) > policy.MaxBytes {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageTooLarge
	}
	if !allowedVidRushImageMIME(mime) {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	config, _, err := digest.DecodeImageConfigBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return scriptports.VerifiedArtifact{}, ErrVidRushImageTooLarge
		}
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	if !policy.AcceptsDimensions(config.Width, config.Height) {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	img, _, err := digest.DecodeImageBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return scriptports.VerifiedArtifact{}, ErrVidRushImageTooLarge
		}
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	if digest.HasPanelGridCollageImage(img) {
		seamsV, seamsH, continuityV, continuityH := digest.PanelGridCollageEvidence(img)
		log.Printf("vidrush image rejected as panel grid collage: seams_v=%d seams_h=%d continuity_v=%.2f continuity_h=%.2f",
			seamsV, seamsH, continuityV, continuityH)
		return scriptports.VerifiedArtifact{}, ErrVidRushImageCollage
	}
	sum := digest.SHA256Bytes(data)
	candidate.ContentSHA256 = sum
	candidate.MIMEType = mime
	candidate.Width = config.Width
	candidate.Height = config.Height
	// The perceptual hash is best-effort: it powers near-duplicate detection
	// across providers, but a decoder that cannot produce one must not fail
	// technical verification.
	if perceptualHash, hashErr := digest.PerceptualHash(img); hashErr == nil {
		candidate.PerceptualHash = perceptualHash
	}
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptports.VerifiedArtifact{
		Candidate:     candidate,
		MIMEType:      mime,
		SizeBytes:     int64(len(data)),
		ContentSHA256: candidate.ContentSHA256,
		Width:         config.Width,
		Height:        config.Height,
		RightsStatus:  candidate.RightsStatus,
	}, nil
}

// VerifyVidRushImageFile is the file-based bridge used by providers that
// stage bytes on disk. It intentionally shares all validation with the byte
// path and never writes database state.
func VerifyVidRushImageFile(candidate scriptpkg.SegmentAssetCandidate, path string, policy VidRushImagePolicy) (scriptports.VerifiedArtifact, error) {
	policy = normalizeVidRushImagePolicy(policy)
	if err := CanonicalizeVidRushImageFile(path, policy); err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	if info.Size() <= 0 || info.Size() > policy.MaxBytes {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageTooLarge
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	mime := http.DetectContentType(data)
	verified, err := VerifyVidRushImageBytes(candidate, data, mime, policy)
	if err != nil {
		return scriptports.VerifiedArtifact{}, err
	}
	verified.LocalPath = path
	verified.Candidate.LocalPath = path
	return verified, nil
}

// CanonicalizeVidRushImageFile normalizes an acquired image in-place before
// it crosses the common finalizer boundary. Opaque images become JPEG;
// images with transparency remain PNG. This keeps the persisted MIME type,
// extension and hash aligned and avoids storing animated/opaque GIF bytes as
// a misleading .jpg artifact.
func CanonicalizeVidRushImageFile(path string, policy VidRushImagePolicy) error {
	policy = normalizeVidRushImagePolicy(policy)
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) == 0 || int64(len(data)) > policy.MaxBytes {
		return ErrVidRushImageTooLarge
	}
	config, _, err := digest.DecodeImageConfigBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return ErrVidRushImageTooLarge
		}
		return ErrVidRushImageInvalid
	}
	if !policy.AcceptsDimensions(config.Width, config.Height) {
		return ErrVidRushImageInvalid
	}
	img, _, err := digest.DecodeImageBounded(data, policy.MaxPixels)
	if err != nil {
		if errors.Is(err, digest.ErrImagePixelBudget) {
			return ErrVidRushImageTooLarge
		}
		return ErrVidRushImageInvalid
	}
	opaque := true
	if probe, ok := img.(interface{ Opaque() bool }); ok {
		opaque = probe.Opaque()
	}
	var encoded bytes.Buffer
	mime := "image/jpeg"
	if !opaque {
		mime = "image/png"
		err = png.Encode(&encoded, img)
	} else {
		err = jpeg.Encode(&encoded, img, &jpeg.Options{Quality: 90})
	}
	if err != nil || encoded.Len() == 0 || int64(encoded.Len()) > policy.MaxBytes {
		return ErrVidRushImageInvalid
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".vidrush-canonical-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(encoded.Bytes()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	_ = mime // MIME is recomputed from the canonical bytes by verification.
	return nil
}

func allowedVidRushImageMIME(mime string) bool {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return true
	default:
		return false
	}
}
