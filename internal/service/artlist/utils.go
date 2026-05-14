package artlist

import (
	"context"
	"fmt"
	"strings"

	driveapi "google.golang.org/api/drive/v3"
	driveutil "velox/go-master/pkg/drive"
)

// getIntFromResult extracts an int from a result map, handling both int and float64 types
func getIntFromResult(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

type artlistChecksumChecker struct {
	driveClient *driveapi.Service
}

func (c *artlistChecksumChecker) GetMD5Checksum(ctx context.Context, driveLink string) (string, error) {
	fileID := driveutil.FileIDFromLink(driveLink)
	if fileID == "" {
		return "", fmt.Errorf("could not extract file ID from link: %s", driveLink)
	}
	file, err := c.driveClient.Files.Get(fileID).Fields("id,md5Checksum").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(file.Md5Checksum), nil
}
