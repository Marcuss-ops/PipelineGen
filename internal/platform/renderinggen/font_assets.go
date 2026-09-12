// font_assets.go owns the canonical, immutable font asset descriptors used by
// the clip-render plan mapper.
//
// A registered font is a process invariant: for the lifetime of the process
// its absolute path, byte size and SHA-256 content digest never change.
// Resolving them once and caching the descriptor removes a full
// os.ReadFile + SHA-256 pass from EVERY clip render (the previous
// poppinsFontAsset()/watermarkFontAsset() helpers re-read and re-hashed the
// font on each mapper call).
//
// Path resolution is likewise done ONCE against a configured asset root, never
// against the process working directory: the old bare "assets/fonts/..." path
// only resolved when the binary happened to be started from the repository
// root, so a systemd-launched worker silently failed to find its fonts.
package renderinggen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

const (
	// FontMontserratBold is the canonical default clip/watermark font.
	FontMontserratBold = "font-montserrat-bold"
	// FontPoppinsBold is the canonical preset font.
	FontPoppinsBold = "font-poppins-bold"
)

// AssetRootEnv overrides the configured asset root. When unset the root is
// discovered once by walking up from the executable (then the CWD) to the
// directory that carries the checked-in asset bundle.
const AssetRootEnv = "PIPELINEGEN_ASSET_ROOT"

// canonicalFonts maps each registered font identity to its asset-root-relative
// path. This map is the single owner of "which file backs which font id"; the
// mapper never hardcodes a font path.
var canonicalFonts = map[string]string{
	FontMontserratBold: "assets/fonts/Montserrat-Bold.ttf",
	FontPoppinsBold:    "assets/fonts/Poppins-Bold.ttf",
}

var (
	assetRootOnce sync.Once
	assetRootPath string

	fontCacheMu sync.Mutex
	fontCache   = map[string]assetRef{}
)

// AssetRoot returns the configured asset root (absolute when resolvable), or
// an empty string when no root could be discovered. Resolution happens once
// per process.
func AssetRoot() string {
	assetRootOnce.Do(func() { assetRootPath = discoverAssetRoot() })
	return assetRootPath
}

func discoverAssetRoot() string {
	if env := strings.TrimSpace(os.Getenv(AssetRootEnv)); env != "" {
		if abs, err := filepath.Abs(env); err == nil {
			return abs
		}
		return env
	}
	if exe, err := os.Executable(); err == nil {
		if root := findAssetRoot(filepath.Dir(exe)); root != "" {
			return root
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if root := findAssetRoot(cwd); root != "" {
			return root
		}
	}
	return ""
}

// findAssetRoot walks up from dir until it finds the directory carrying the
// checked-in asset bundle (assets/fonts).
func findAssetRoot(dir string) string {
	for {
		if info, err := os.Stat(filepath.Join(dir, "assets", "fonts")); err == nil && info.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ResolveFontAsset returns the immutable descriptor for a registered font id:
// its content digest is the asset identity, LocalPath is absolute, and the
// descriptor is computed at most once per process. Fail-closed: an unknown id
// or a missing asset is a typed error, never a silently empty ref.
func ResolveFontAsset(id string) (assetRef, error) {
	rel, ok := canonicalFonts[id]
	if !ok {
		return assetRef{}, fmt.Errorf("unknown font asset id %q", id)
	}
	fontCacheMu.Lock()
	defer fontCacheMu.Unlock()
	if cached, ok := fontCache[rel]; ok {
		return cached, nil
	}
	root := AssetRoot()
	if root == "" {
		return assetRef{}, fmt.Errorf("clip plan mapper: asset root not found for %s (set %s)", rel, AssetRootEnv)
	}
	localPath := filepath.Join(root, rel)
	b, err := os.ReadFile(localPath)
	if err != nil {
		return assetRef{}, fmt.Errorf("read %s: %w", localPath, err)
	}
	ref := assetRef{
		Hash:        digest.SHA256Bytes(b),
		LogicalPath: hashAddressedPath(id, filepath.Base(rel)),
		LocalPath:   localPath,
	}
	fontCache[rel] = ref
	return ref, nil
}

// resetFontAssetCache drops the cached descriptors and the discovered asset
// root. It exists for tests that manipulate the asset root / working
// directory; production code resolves the root exactly once.
func resetFontAssetCache() {
	fontCacheMu.Lock()
	fontCache = map[string]assetRef{}
	fontCacheMu.Unlock()
	assetRootOnce = sync.Once{}
}
