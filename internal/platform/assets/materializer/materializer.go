// Package materializer is the ONE owner of the question every asset consumer
// used to answer for itself: "are these bytes the bytes this content address
// names, and where do I get them if they are not?".
//
// Before this package the answer was implemented twice, differently:
//
//   - internal/platform/overlays/cache.go::EnsureAsset downloaded a URL,
//     verified the digest with crypto/sha256, cached the file under its
//     address — and then trusted the cache entry forever, because `Has` only
//     stat'ed the file. A truncated or replaced cache file rendered silently.
//   - internal/platform/renderinggen/queue_asset_prefetch.go picked a
//     producer-supplied local path OR a URL and pushed the bytes into the
//     rendering object store, originally WITHOUT any verification at all.
//
// Two implementations of one invariant is how a system ends up disagreeing
// with itself: one path verifies on write and trusts on read, the other trusts
// on write, and neither agrees on what happens when the bytes are wrong. The
// reported production failure — a media row whose sha256 said one thing while
// its local_path said another — is exactly the shape of that disagreement.
//
// The contract here is deliberately small, because it must be the only thing
// callers can use:
//
//	Ref      = asset_id + sha256        (identity; the ONLY durable input)
//	Source   = url + local_path         (optional fallbacks; never an identity)
//	Target   = a destination the caller owns (a cache root, an object store)
//	Ensure   = materialize VerifiedBytes into a Target, or fail closed
//
// The rules, all of them enforceable:
//
//  1. Nothing is installed under an address unless its bytes hash to that
//     address. A mismatch leaves no file behind.
//  2. A producer-supplied local path is an OPTIMIZATION, verified before use.
//     An unverifiable hint falls through to the fetchable origin instead of
//     failing the caller (a stale local path is an expected event — tmpfs
//     sweeps happen — while wrong bytes under a right address are corruption).
//  3. A cache HIT is re-verified. This is the one place the two legacy paths
//     both got wrong: trusting a file because it exists converts a disk fault
//     or a partial write into a silently wrong render.
//  4. When no source can produce the addressed bytes, the error is typed
//     (ErrSourceUnavailable) and names the address, so it is attributable
//     rather than a downstream mystery.
//
// Verification is delegated to internal/kernel/digest, the repository's single
// hashing authority (godlike/06): this package owns WHEN to verify, never HOW.
package materializer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// ── Typed failures (godlike/07 fail-closed) ──────────────────────────────

var (
	// ErrInvalidRef is returned when the identity itself is unusable (no
	// content address). A ref without an address cannot be materialized,
	// cached or verified, so it is a caller error.
	ErrInvalidRef = errors.New("asset materializer: invalid content-addressed ref")

	// ErrSourceUnavailable is returned when no source produced the addressed
	// bytes: every hint was rejected and no fetchable origin was provided or
	// reachable. It means the producer's contract is broken, not that a cache
	// was cold — the distinction that used to be lost as a silent skip.
	ErrSourceUnavailable = errors.New("asset materializer: no source produced the addressed bytes")

	// ErrHashMismatch is returned when bytes were offered for an address and
	// did not hash to it. Nothing is installed.
	ErrHashMismatch = errors.New("asset materializer: bytes do not match their content address")

	// ErrCorruptCache is returned when a previously materialized file no
	// longer matches its own address and no source is available to repair it.
	ErrCorruptCache = errors.New("asset materializer: cached bytes do not match their content address")
)

// Ref is the content-addressed identity of an asset. It is the only durable
// input a caller should have: everything else about the asset (where it lives,
// who published it) is a location, and locations belong to the location
// registry, not to the thing that identifies the asset.
type Ref struct {
	// AssetID is the human-legible id, used only in error messages.
	AssetID string
	// SHA256 is the content address (hex, case-insensitive).
	SHA256 string
}

// Address normalizes the content address for comparison and storage.
func (r Ref) Address() string { return strings.ToLower(strings.TrimSpace(r.SHA256)) }

// Source describes where the bytes MAY be obtained. Both fields are optional
// fallbacks: a source is never an identity, and an unusable one is skipped
// rather than trusted.
type Source struct {
	// LocalPath holds bytes already produced by this process. It is verified
	// before use, so a stale path is a no-op instead of a corrupt write.
	LocalPath string
	// URL is a fetchable origin (http/https). Non-HTTP values are treated as
	// non-fetchable logical paths and skipped.
	URL string
}

// Origin names where the materialized bytes came from, so a caller can report
// (and test) which fallback was actually used.
type Origin string

const (
	// OriginCache means the bytes were already materialized and still verify.
	OriginCache Origin = "cache"
	// OriginLocal means they were taken from a verified producer-local file.
	OriginLocal Origin = "local"
	// OriginURL means they were downloaded from the fetchable origin.
	OriginURL Origin = "url"
)

// Materialized is the verified result of materialization.
type Materialized struct {
	SHA256 string
	Path   string
	Size   int64
	Origin Origin
}

// Options configures the materializer.
type Options struct {
	// Client is the HTTP client used for fetchable origins. A nil client
	// selects DefaultClient.
	Client *http.Client
	// Verifier is the file-content authority. A nil verifier uses
	// digest.NewVerifier(nil) — the canonical implementation, memoized.
	Verifier *digest.Verifier
}

// DefaultClient bounds every materialization fetch. The legacy overlay cache
// used http.DefaultClient, which has NO timeout: a hanging origin pinned the
// caller — and whatever lease it held — forever.
var DefaultClient = &http.Client{Timeout: 10 * time.Minute}

// Materializer owns content verification and source selection.
type Materializer struct {
	client *http.Client
	verify *digest.Verifier
}

// New returns a materializer. It never fails: every option has a canonical
// default, because a materializer that cannot be constructed would push the
// "which default?" decision back onto every caller — the duplication this
// package exists to remove.
func New(opts Options) *Materializer {
	client := opts.Client
	if client == nil {
		client = DefaultClient
	}
	verifier := opts.Verifier
	if verifier == nil {
		verifier = digest.NewVerifier(nil)
	}
	return &Materializer{client: client, verify: verifier}
}

// Matches reports whether the file at path holds the bytes named by sha256.
// Every failure mode — empty address, empty path, missing file, directory,
// unreadable file, digest mismatch — answers false, so a caller can never read
// a "probably fine" from it. Only a verified digest answers true.
func (m *Materializer) Matches(path, sha256 string) bool {
	address := strings.ToLower(strings.TrimSpace(sha256))
	if address == "" {
		return false
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	sum, _, err := m.verify.Verify(path)
	if err != nil {
		return false
	}
	return strings.EqualFold(sum, address)
}

// Digest returns the verified digest and size of the file at path. It is the
// only way a caller should turn a file into an address.
func (m *Materializer) Digest(path string) (string, int64, error) {
	if strings.TrimSpace(path) == "" {
		return "", 0, fmt.Errorf("%w: empty path", ErrInvalidRef)
	}
	return m.verify.Verify(path)
}

// Dest is the canonical destination for an address under a root:
//
//	<root>/<addr[:2]>/<addr>/asset.bin
//
// It is exported so every caller shares ONE on-disk layout. A caller with a
// pre-existing layout (the overlay cache namespaces its files under
// `assets/`) passes its own destination to Ensure instead, so a unification
// never silently invalidates caches that are already on disk.
func Dest(root, sha256Hex string) string {
	address := strings.ToLower(strings.TrimSpace(sha256Hex))
	if len(address) < 2 {
		return ""
	}
	return filepath.Join(root, address[:2], address, "asset.bin")
}

// Ensure materializes ref at dest, using src as fallback. dest is the exact
// file path the caller wants the verified bytes to end up at; the returned
// path is guaranteed to hold the addressed bytes.
//
// Order of preference, all verified:
//
//  1. the existing file at dest, when it still hashes correctly;
//  2. src.LocalPath, when its bytes hash correctly;
//  3. src.URL, downloaded and verified while streaming.
//
// A destination file that does NOT hash correctly is removed and repaired from
// a source; when no source can produce the address the error is ErrCorruptCache
// (the bytes are wrong, and repairing is impossible) rather than a silent pass.
func (m *Materializer) Ensure(ctx context.Context, ref Ref, src Source, dest string) (Materialized, error) {
	address := ref.Address()
	if !validAddress(address) {
		return Materialized{}, fmt.Errorf("%w: asset %q address %q", ErrInvalidRef, ref.AssetID, ref.SHA256)
	}
	if strings.TrimSpace(dest) == "" {
		return Materialized{}, fmt.Errorf("%w: destination path is empty", ErrInvalidRef)
	}

	if fileExists(dest) {
		if m.Matches(dest, address) {
			size, _ := fileSize(dest)
			return Materialized{SHA256: address, Path: dest, Size: size, Origin: OriginCache}, nil
		}
		// Wrong bytes under the right address: never reused, and never left
		// behind to be trusted by someone else.
		_ = os.Remove(dest)
	}

	if localPath := strings.TrimSpace(src.LocalPath); localPath != "" && m.Matches(localPath, address) {
		size, err := m.installFromFile(dest, localPath, address)
		if err != nil {
			return Materialized{}, err
		}
		return Materialized{SHA256: address, Path: dest, Size: size, Origin: OriginLocal}, nil
	}

	origin := strings.TrimSpace(src.URL)
	if !strings.HasPrefix(origin, "http") {
		if _, corrupt := os.Stat(dest); corrupt == nil {
			return Materialized{}, fmt.Errorf("%w: asset %q address %s (corrupt destination, no fetchable source)",
				ErrCorruptCache, ref.AssetID, address)
		}
		return Materialized{}, fmt.Errorf("%w: asset %q address %s (local hint %q rejected, origin %q not fetchable)",
			ErrSourceUnavailable, ref.AssetID, address, src.LocalPath, src.URL)
	}

	size, err := m.installFromURL(ctx, dest, origin, address)
	if err != nil {
		return Materialized{}, fmt.Errorf("asset %s: %w", address, err)
	}
	return Materialized{SHA256: address, Path: dest, Size: size, Origin: OriginURL}, nil
}

// installFromFile copies a verified local file into place.
func (m *Materializer) installFromFile(dest, source, address string) (int64, error) {
	f, err := os.Open(source)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return m.install(dest, f, address, info.Size())
}

// installFromURL streams a fetchable origin into place, hashing as it writes.
func (m *Materializer) installFromURL(ctx context.Context, dest, origin, address string) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, origin, nil)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", ErrSourceUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("%w: HTTP %d", ErrSourceUnavailable, resp.StatusCode)
	}
	// ContentLength may be -1 for chunked responses; install then streams
	// without a length assertion instead of failing a valid download.
	return m.install(dest, resp.Body, address, resp.ContentLength)
}

// install streams r into dest through a temporary file in the same directory,
// verifying the digest BEFORE the replace. Nothing is visible under dest until
// the bytes are known to be the addressed bytes, a length mismatch is a
// failure, and a rejected install leaves no temporary file behind.
func (m *Materializer) install(dest string, r io.Reader, address string, size int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".part-*")
	if err != nil {
		return 0, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	hasher := digest.NewSHA256()
	written, copyErr := io.Copy(io.MultiWriter(tmp, hasher), r)
	if copyErr == nil && size >= 0 && written != size {
		copyErr = fmt.Errorf("content length mismatch: got %d want %d", written, size)
	}
	if closeErr := tmp.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return 0, copyErr
	}

	if got := fmt.Sprintf("%x", hasher.Sum(nil)); !strings.EqualFold(got, address) {
		return 0, fmt.Errorf("%w: address %s, bytes %s", ErrHashMismatch, address, got)
	}
	if err := os.Rename(tmpPath, dest); err != nil {
		return 0, err
	}
	return written, nil
}

// validAddress accepts a hex content address of at least two characters. The
// length is deliberately not pinned to 64: the overlay cache has always
// addressed assets by whatever digest the plan carried, and tightening the
// contract here would break live plans rather than wrong bytes. What is pinned
// is that the address is hex and that the bytes must hash to it.
func validAddress(address string) bool {
	if len(address) < 2 {
		return false
	}
	for i := 0; i < len(address); i++ {
		c := address[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}
