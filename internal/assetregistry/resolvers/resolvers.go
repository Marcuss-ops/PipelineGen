// Package resolvers provides URI-scheme content resolvers for the asset registry.
// Each resolver handles a specific scheme (file, velox-asset, https, drive) and
// enforces security boundaries (SSRF prevention, path traversal protection).
package resolvers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/assetregistry"
)

// ── Shared SSRF Protection ─────────────────────────────────────────────

// blockedIPBlocks contains CIDR ranges that HTTP resolvers must reject.
var blockedIPBlocks []*net.IPNet

func init() {
	blocks := []string{
		"0.0.0.0/8",      // Current network (RFC 1122)
		"10.0.0.0/8",     // Private A
		"100.64.0.0/10",  // Carrier-grade NAT (RFC 6598)
		"127.0.0.0/8",    // Loopback
		"169.254.0.0/16", // Link-local
		"172.16.0.0/12",  // Private B
		"192.168.0.0/16", // Private C
		"198.18.0.0/15",  // Benchmarking (RFC 2544)
		"::1/128",         // IPv6 loopback
		"fe80::/10",       // IPv6 link-local
		"fc00::/7",        // IPv6 unique local
	}
	for _, cidr := range blocks {
		_, block, _ := net.ParseCIDR(cidr)
		if block != nil {
			blockedIPBlocks = append(blockedIPBlocks, block)
		}
	}
}

// isBlockedIP checks if an IP address is in a blocked range.
func isBlockedIP(ip net.IP) bool {
	for _, block := range blockedIPBlocks {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}

// isBlockedHost checks if a hostname resolves to a blocked address.
// Also blocks cloud metadata endpoints.
func isBlockedHost(rawHost string) error {
	// Block cloud metadata hostnames
	lowerHost := strings.ToLower(rawHost)
	blockedHosts := []string{
		"metadata.google.internal",
		"169.254.169.254",
		"instance-data.ec2",
		"metadata.tencentyun.com",
	}
	for _, h := range blockedHosts {
		if strings.Contains(lowerHost, h) {
			return fmt.Errorf("host %q is blocked (metadata endpoint)", rawHost)
		}
	}

	// Resolve hostname
	ips, err := net.LookupIP(rawHost)
	if err != nil {
		return fmt.Errorf("failed to resolve host %q: %w", rawHost, err)
	}
	for _, ip := range ips {
		if isBlockedIP(ip) {
			return fmt.Errorf("host %q resolves to blocked IP %s", rawHost, ip.String())
		}
	}
	return nil
}

// validateRedirect checks each redirect in the chain.
func validateRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return fmt.Errorf("too many redirects")
	}
	host := req.URL.Hostname()
	return isBlockedHost(host)
}

// ── Local Resolver ─────────────────────────────────────────────────────

// LocalResolver resolves file:// URIs for admin imports and development.
type LocalResolver struct {
	allowedDirs []string // directories files may be read from
}

// NewLocalResolver creates a local filesystem resolver with path traversal protection.
func NewLocalResolver(allowedDirs []string) *LocalResolver {
	return &LocalResolver{allowedDirs: allowedDirs}
}

// Scheme returns "file".
func (r *LocalResolver) Scheme() string { return "file" }

// Open opens a local file for reading with path traversal protection.
func (r *LocalResolver) Open(ctx context.Context, ref assetregistry.Reference) (io.ReadCloser, error) {
	cleanPath := assetregistry.CleanAssetPath(ref.Raw)
	if !assetregistry.IsSafePath(r.allowedDirs, cleanPath) {
		return nil, fmt.Errorf("path traversal blocked: %s", ref.Raw)
	}
	return os.Open(cleanPath)
}

// Stat returns file metadata.
func (r *LocalResolver) Stat(ctx context.Context, ref assetregistry.Reference) (assetregistry.ObjectInfo, error) {
	cleanPath := assetregistry.CleanAssetPath(ref.Raw)
	if !assetregistry.IsSafePath(r.allowedDirs, cleanPath) {
		return assetregistry.ObjectInfo{}, fmt.Errorf("path traversal blocked: %s", ref.Raw)
	}
	info, err := os.Stat(cleanPath)
	if err != nil {
		return assetregistry.ObjectInfo{}, err
	}
	return assetregistry.ObjectInfo{
		SizeBytes: info.Size(),
		MimeType:  "",
	}, nil
}

// ── VeloxAsset Resolver ────────────────────────────────────────────────

// VeloxAssetResolver resolves velox-asset:// URIs from the asset registry database.
type VeloxAssetResolver struct {
	db *sql.DB
}

// NewVeloxAssetResolver creates a resolver that looks up assets from the registry DB.
func NewVeloxAssetResolver(db *sql.DB) *VeloxAssetResolver {
	return &VeloxAssetResolver{db: db}
}

// Scheme returns "velox-asset".
func (r *VeloxAssetResolver) Scheme() string { return "velox-asset" }

// Open reads an asset by looking up its storage key from the assets table.
func (r *VeloxAssetResolver) Open(ctx context.Context, ref assetregistry.Reference) (io.ReadCloser, error) {
	var storageBackend, storageKey string
	err := r.db.QueryRowContext(ctx,
		`SELECT storage_backend, storage_key FROM assets WHERE asset_id = ? AND status = 'READY'`,
		ref.AssetID,
	).Scan(&storageBackend, &storageKey)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("asset not found: %s", ref.AssetID)
		}
		return nil, fmt.Errorf("asset lookup failed: %w", err)
	}

	// For local storage backend, open the file
	if storageBackend == "local" {
		return os.Open(storageKey)
	}
	return nil, fmt.Errorf("unsupported storage backend: %s", storageBackend)
}

// Stat returns asset metadata from the database.
func (r *VeloxAssetResolver) Stat(ctx context.Context, ref assetregistry.Reference) (assetregistry.ObjectInfo, error) {
	var sha256 string
	var sizeBytes int64
	var mimeType sql.NullString
	err := r.db.QueryRowContext(ctx,
		`SELECT sha256, size_bytes, mime_type FROM assets WHERE asset_id = ? AND status = 'READY'`,
		ref.AssetID,
	).Scan(&sha256, &sizeBytes, &mimeType)
	if err != nil {
		if err == sql.ErrNoRows {
			return assetregistry.ObjectInfo{}, fmt.Errorf("asset not found: %s", ref.AssetID)
		}
		return assetregistry.ObjectInfo{}, fmt.Errorf("asset stat failed: %w", err)
	}
	return assetregistry.ObjectInfo{
		SHA256:    sha256,
		SizeBytes: sizeBytes,
		MimeType:  mimeType.String,
	}, nil
}

// ── HTTP Resolver ──────────────────────────────────────────────────────

// HTTPResolver resolves https:// URIs with SSRF protection.
type HTTPResolver struct {
	client *http.Client
}

// NewHTTPResolver creates an HTTP resolver with SSRF protection.
func NewHTTPResolver() *HTTPResolver {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				host = addr
			}
			ips, err := net.LookupIP(host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if isBlockedIP(ip) {
					return nil, fmt.Errorf("blocked IP: %s", ip.String())
				}
			}
			dialer := &net.Dialer{}
			return dialer.DialContext(ctx, network, addr)
		},
	}
	return &HTTPResolver{
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: validateRedirect,
		},
	}
}

// Scheme returns "https".
func (r *HTTPResolver) Scheme() string { return "https" }

// Open fetches content from an HTTPS URL with SSRF protection.
func (r *HTTPResolver) Open(ctx context.Context, ref assetregistry.Reference) (io.ReadCloser, error) {
	u, err := url.Parse(ref.Raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return nil, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	host := u.Hostname()
	if err := isBlockedHost(host); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "GET", ref.Raw, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	// Wrap body to close on read completion
	return &httpReadCloser{resp.Body, resp}, nil
}

// Stat performs a HEAD request to get metadata.
func (r *HTTPResolver) Stat(ctx context.Context, ref assetregistry.Reference) (assetregistry.ObjectInfo, error) {
	u, err := url.Parse(ref.Raw)
	if err != nil {
		return assetregistry.ObjectInfo{}, err
	}
	if u.Scheme != "https" {
		return assetregistry.ObjectInfo{}, fmt.Errorf("unsupported scheme: %s", u.Scheme)
	}

	host := u.Hostname()
	if err := isBlockedHost(host); err != nil {
		return assetregistry.ObjectInfo{}, err
	}

	req, err := http.NewRequestWithContext(ctx, "HEAD", ref.Raw, nil)
	if err != nil {
		return assetregistry.ObjectInfo{}, err
	}

	resp, err := r.client.Do(req)
	if err != nil {
		return assetregistry.ObjectInfo{}, err
	}
	resp.Body.Close()

	sizeBytes, _ := strconv.ParseInt(resp.Header.Get("Content-Length"), 10, 64)
	return assetregistry.ObjectInfo{
		MimeType:  resp.Header.Get("Content-Type"),
		SizeBytes: sizeBytes,
	}, nil
}

type httpReadCloser struct {
	io.ReadCloser
	resp *http.Response
}

func (h *httpReadCloser) Close() error {
	return h.resp.Body.Close()
}

// ── Drive Resolver ─────────────────────────────────────────────────────

// DriveResolver resolves drive:// URIs via the Google Drive API.
// Placeholder for drive integration. In the initial PR-7 cutover,
// drive assets flow through the existing ingest pipeline.
type DriveResolver struct{}

// NewDriveResolver creates a Drive content resolver.
func NewDriveResolver() *DriveResolver {
	return &DriveResolver{}
}

// Scheme returns "drive".
func (r *DriveResolver) Scheme() string { return "drive" }

// Open resolves a Drive URI placeholder.
func (r *DriveResolver) Open(ctx context.Context, ref assetregistry.Reference) (io.ReadCloser, error) {
	return nil, fmt.Errorf("drive resolver not yet implemented for %s", ref.Raw)
}

// Stat resolves Drive URI metadata placeholder.
func (r *DriveResolver) Stat(ctx context.Context, ref assetregistry.Reference) (assetregistry.ObjectInfo, error) {
	return assetregistry.ObjectInfo{}, fmt.Errorf("drive resolver not yet implemented for %s", ref.Raw)
}
