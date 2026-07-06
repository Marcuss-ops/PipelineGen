package drive

import (
	"context"
	"fmt"
)

// Admin returns the Uploader itself as a drive.Admin interface.
// Convenience method so callers holding *Uploader can pass it to
// functions accepting drive.Admin without a separate variable.
func (u *Uploader) Admin() Admin { return u }

// Ping verifies the Drive service is reachable by calling About.Get.
// Implemented as a single canonical API call so the readiness barrier
// can exercise the liveness contract without touching the file surface.
func (u *Uploader) Ping(ctx context.Context) error {
	if u.Service == nil {
		return fmt.Errorf("drive service not configured")
	}
	_, err := u.Service.About.Get().Fields("user").Context(ctx).Do()
	return err
}
