// Deprecated: use pkg/urlutil.ExtractVideoID instead.
// This file delegates to the canonical implementation in pkg/urlutil/.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Deprecated: use pkg/urlutil.ExtractVideoID.
func ExtractVideoID(rawURL string) (string, error) {
	return urlutil.ExtractVideoID(rawURL)
}
