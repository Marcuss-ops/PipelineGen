package overlays

import (
	"path"
	"strings"
)

// logicalAssetPath derives the workspace-relative logical path for an asset
// URL. Assets land under the workspace assets root (the worker materializes
// them there and the plan references resolve relative to it).
func logicalAssetPath(url string) string {
	base := path.Base(strings.TrimSpace(url))
	if base == "." || base == "/" || base == "" {
		return "assets/asset"
	}
	return "assets/" + base
}
