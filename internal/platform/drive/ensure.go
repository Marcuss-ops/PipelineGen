package drive

import (
	"context"
	"fmt"
)

// EnsureFolderPath walks a list of segments under rootID, creating each folder
// if it doesn't exist. Returns the final folder ID.
//
// FASE 9 Step 6 (June 2026): migrated from *Uploader to Admin (Pattern 0 port).
// Only uses GetOrCreateFolder — fully covered by the Admin interface.
//
// This replaces duplicated GetOrCreateFolder loops in Artlist, Stock, Ingest,
// and Google Accounting.
func EnsureFolderPath(ctx context.Context, admin Admin, rootID string, segments ...string) (string, error) {
	if admin == nil {
		return rootID, nil
	}
	currentID := rootID
	for i, seg := range segments {
		if seg == "" {
			continue
		}
		id, err := admin.GetOrCreateFolder(ctx, seg, currentID)
		if err != nil {
			return "", fmt.Errorf("ensure folder %q (segment %d of %d): %w", seg, i+1, len(segments), err)
		}
		currentID = id
	}
	return currentID, nil
}
