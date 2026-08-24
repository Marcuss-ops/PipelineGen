package adapters

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

const vidRushImageUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

var (
	ErrVidRushImageTooLarge = errors.New("vidrush image exceeds download limit")
	ErrVidRushImageInvalid  = errors.New("vidrush image is not a valid supported image")
	ErrVidRushSSRFBlocked   = errors.New("vidrush remote image URL blocked by SSRF policy")
)

// VidRushImagePolicy is deliberately conservative and CPU-friendly. The
// policy is shared by web-image and generated-image verification.
type VidRushImagePolicy struct {
	MaxBytes     int64
	MinWidth     int
	MinHeight    int
	Timeout      time.Duration
	MaxRedirects int
}

func DefaultVidRushImagePolicy() VidRushImagePolicy {
	return VidRushImagePolicy{MaxBytes: 20 << 20, MinWidth: 640, MinHeight: 360, Timeout: 15 * time.Second, MaxRedirects: 3}
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
	if policy.Timeout <= 0 {
		policy.Timeout = defaults.Timeout
	}
	if policy.MaxRedirects < 0 {
		policy.MaxRedirects = defaults.MaxRedirects
	}
	return policy
}

// ValidateVidRushRemoteImageURL rejects non-web schemes, credentials,
// localhost and literal private/link-local addresses before any request is
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
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "metadata.google.internal" {
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

// DownloadVidRushImage applies the size, MIME, timeout and redirect policy.
// It returns bytes only; callers hand those bytes to the common staging/
// finalization path rather than inserting media_assets themselves.
func DownloadVidRushImage(ctx context.Context, client *http.Client, rawURL string, policy VidRushImagePolicy) ([]byte, string, error) {
	return downloadVidRushImage(ctx, client, rawURL, "", policy)
}

// DownloadVidRushImageForCandidate carries the result page as a referrer when
// the image host requires a normal browser-like navigation context. The image
// URL itself still goes through the same SSRF, size, MIME and redirect guards.
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
	// Do not inherit environment proxies or a caller transport that can
	// bypass the DNS/IP policy. The dialer resolves and validates the exact
	// address immediately before connecting, closing the DNS-rebinding gap
	// between the preflight lookup and the actual socket dial.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if base, ok := client.Transport.(*http.Transport); ok && base != nil {
		transport = base.Clone()
	}
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: policy.Timeout, KeepAlive: 30 * time.Second}
	transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("vidrush image: invalid upstream address: %w", err)
		}
		if err := validateVidRushResolvedHost(dialCtx, host); err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(dialCtx, host)
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
			return dialer.DialContext(dialCtx, network, net.JoinHostPort(resolved.IP.String(), port))
		}
		return nil, ErrVidRushSSRFBlocked
	}
	copyClient.Transport = transport
	copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > policy.MaxRedirects {
			return fmt.Errorf("vidrush image: maximum redirects exceeded")
		}
		if err := ValidateVidRushRemoteImageURL(req.URL.String()); err != nil {
			return err
		}
		return validateVidRushResolvedHost(req.Context(), req.URL.String())
	}
	request, err := newVidRushImageRequest(ctx, rawURL, sourcePageURL)
	if err != nil {
		return nil, "", err
	}
	response, err := copyClient.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()
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

func newVidRushImageRequest(ctx context.Context, rawURL, sourcePageURL string) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", vidRushImageUserAgent)
	request.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
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
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < policy.MinWidth || config.Height < policy.MinHeight {
		return scriptports.VerifiedArtifact{}, ErrVidRushImageInvalid
	}
	sum := digest.SHA256Bytes(data)
	candidate.LegacyFileMD5 = sum
	candidate.MIMEType = mime
	candidate.Width = config.Width
	candidate.Height = config.Height
	candidate.AcquisitionStatus = scriptpkg.VidRushStatusAcquired
	candidate.VerificationStatus = scriptpkg.VidRushStatusVerified
	return scriptports.VerifiedArtifact{
		Candidate:     candidate,
		MIMEType:      mime,
		SizeBytes:     int64(len(data)),
		LegacyFileMD5: candidate.LegacyFileMD5,
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
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
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
	defer os.Remove(tmpName)
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
	case "image/jpeg", "image/png", "image/gif":
		return true
	default:
		return false
	}
}
