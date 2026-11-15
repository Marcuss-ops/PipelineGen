package entitycatalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// VerifiedAssetRegistryEnv points at the reviewed local asset registry.
// Keeping the variable here makes entitycatalog the single owner of the
// registry contract; presentation code only consumes the verified entries.
const VerifiedAssetRegistryEnv = "VELOX_ENTITY_ASSET_REGISTRY"

// verifiedAssetRegistryCandidates are deployment-local defaults. They mirror
// the historical scripts-side search order; only the owner changed.
var verifiedAssetRegistryCandidates = []string{
	"config/entity_asset_registry.json",
	"refactored/config/entity_asset_registry.json",
	"../ops/assets-store/entity-asset-registry.json",
	"ops/assets-store/entity-asset-registry.json",
}

// verifiedAssetManifest is the reviewed bridge between a named identity and a
// local file whose bytes have already been checked by an operator. A hash in
// the semantic bundle proves that some bytes exist; it does not prove that the
// picture depicts the person or place. This registry is the semantic gate.
type verifiedAssetManifest struct {
	Version  int                             `json:"version"`
	Entities map[string][]verifiedAssetEntry `json:"entities"`
}

type verifiedAssetEntry struct {
	AssetID     string `json:"asset_id"`
	SHA256      string `json:"sha256"`
	Path        string `json:"path"`
	MediaType   string `json:"media_type"`
	DriveFileID string `json:"drive_file_id,omitempty"`
	DriveLink   string `json:"drive_link,omitempty"`
}

// VerifiedAsset is one content-addressed file an operator has reviewed for an
// identity. LocalPath is runtime input to the overlay planner; URL is the
// canonical downloadable locator when the registry supplies a Drive fact.
type VerifiedAsset struct {
	AssetID   string
	SHA256    string
	MediaType string
	URL       string
	LocalPath string
}

// VerifiedAssetRegistry maps a normalized identity key to its reviewed assets.
type VerifiedAssetRegistry map[string][]VerifiedAsset

// LoadVerifiedAssetRegistry reads the configured registry and re-hashes every
// entry. A missing registry disables the feature (nil, nil); an explicitly
// configured unreadable or structurally invalid registry fails closed. An
// individual stale or modified file is skipped, never rendered.
func LoadVerifiedAssetRegistry() (VerifiedAssetRegistry, error) {
	path := strings.TrimSpace(os.Getenv(VerifiedAssetRegistryEnv))
	if path == "" {
		for _, candidate := range verifiedAssetRegistryCandidates {
			if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
				path = candidate
				break
			}
		}
	}
	if path == "" {
		return nil, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("verified asset registry: read %s: %w", path, err)
	}
	var manifest verifiedAssetManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("verified asset registry: decode %s: %w", path, err)
	}
	if manifest.Version != 1 {
		return nil, fmt.Errorf("verified asset registry: unsupported version %d in %s", manifest.Version, path)
	}
	baseDir := filepath.Dir(path)
	registry := make(VerifiedAssetRegistry, len(manifest.Entities))
	for name, entries := range manifest.Entities {
		key := NormalizeVerifiedAssetKey(name)
		if key == "" {
			continue
		}
		for _, entry := range entries {
			ref, ok := verifyVerifiedAssetEntry(baseDir, entry)
			if !ok {
				continue
			}
			registry[key] = append(registry[key], ref)
		}
	}
	if len(registry) == 0 {
		return nil, nil
	}
	return registry, nil
}

func verifyVerifiedAssetEntry(baseDir string, entry verifiedAssetEntry) (VerifiedAsset, bool) {
	sha := strings.ToLower(strings.TrimSpace(entry.SHA256))
	if !IsValidContentSHA256(sha) || strings.TrimSpace(entry.Path) == "" {
		return VerifiedAsset{}, false
	}
	path := strings.TrimSpace(entry.Path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(baseDir, path)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return VerifiedAsset{}, false
	}
	sum, _, err := digest.SHA256File(path)
	if err != nil || !strings.EqualFold(sum, sha) {
		return VerifiedAsset{}, false
	}
	assetID := strings.TrimSpace(entry.AssetID)
	if assetID == "" {
		assetID = sum
	}
	mediaType := strings.TrimSpace(entry.MediaType)
	if mediaType == "" {
		mediaType = verifiedAssetMediaType(path)
	}
	resourceURL := strings.TrimSpace(entry.DriveLink)
	if driveID := strings.TrimSpace(entry.DriveFileID); driveID != "" {
		resourceURL = "https://drive.google.com/uc?export=download&id=" + url.QueryEscape(driveID)
	}
	return VerifiedAsset{
		AssetID:   assetID,
		URL:       resourceURL,
		SHA256:    sum,
		MediaType: mediaType,
		LocalPath: path,
	}, true
}

func verifiedAssetMediaType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		return "image/jpeg"
	}
}

// NormalizeVerifiedAssetKey makes apostrophes, accents and punctuation
// irrelevant for the identity join: "L’Associated Press" and
// "l associated press" must address the same registry row.
func NormalizeVerifiedAssetKey(value string) string {
	value = strings.NewReplacer("’", "'", "‘", "'", "`", "'").Replace(strings.ToLower(strings.TrimSpace(value)))
	var out strings.Builder
	space := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			space = false
			continue
		}
		if !space && out.Len() > 0 {
			out.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(out.String())
}
