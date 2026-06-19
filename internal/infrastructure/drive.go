// Deprecated: use pkg/urlutil.FileIDFromDriveLink instead.
// This file delegates to the canonical implementation in pkg/urlutil/.
package platform

import (
	"github.com/Marcuss-ops/PipelineGen/pkg/urlutil"
)

// Deprecated: use pkg/urlutil.FileIDFromDriveLink.
func FileIDFromDriveLink(rawLink string) (string, error) {
	return urlutil.FileIDFromDriveLink(rawLink)
}
