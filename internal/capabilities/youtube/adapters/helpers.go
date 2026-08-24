// Package youtube — leaf forwarding wrappers to tagutil (CPR-CC-6 split, June 2026).
package adapters

import (
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
)

// getGroupFromDestination extracts group name from destination request.
func getGroupFromDestination(dest *youtubetypes.DestinationRequest) string {
	if dest == nil {
		return ""
	}
	return dest.Group
}

// canonicalYouTubeURL delegates to the canonical implementation in tagutil.

// validateDownloadURL delegates to the canonical implementation in tagutil.

// fallbackMD5String delegates to the canonical implementation in tagutil.

// fallbackMD5File delegates to the canonical implementation in tagutil.
