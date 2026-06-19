// Package timeutil provides RFC3339 timestamp helpers.
//
// Deprecated: use pkg/timeutil instead. This file delegates every exported
// function to the canonical implementation in pkg/timeutil/ so that call
// sites can be migrated incrementally without breaking parallel PRs.
package platform

import (
	"time"

	pkgtimeutil "github.com/Marcuss-ops/PipelineGen/pkg/timeutil"
)

// Deprecated: use pkg/timeutil.Now.
func Now() time.Time { return pkgtimeutil.Now() }

// Deprecated: use pkg/timeutil.ParseRFC3339.
func ParseRFC3339(s string) time.Time { return pkgtimeutil.ParseRFC3339(s) }

// Deprecated: use pkg/timeutil.ParseRFC3339String.
func ParseRFC3339String(s string) time.Time { return pkgtimeutil.ParseRFC3339(s) }

// Deprecated: use pkg/timeutil.ParseRFC3339PtrString.
func ParseRFC3339PtrString(s *string) *time.Time { return pkgtimeutil.ParseRFC3339PtrString(s) }

// Deprecated: use pkg/timeutil.FormatRFC3339.
func FormatRFC3339(t time.Time) string { return pkgtimeutil.FormatRFC3339(t) }

// Deprecated: use pkg/timeutil.FormatPtrRFC3339.
func FormatPtrRFC3339(t *time.Time) any { return pkgtimeutil.FormatPtrRFC3339(t) }

// Deprecated: use pkg/timeutil.ParseRFC3339Ptr.
func ParseRFC3339Ptr(s string) *time.Time { return pkgtimeutil.ParseRFC3339Ptr(s) }

// DerefString returns the value of a *string or "" if nil.
// Deprecated: use pkg/ptrutil.DerefOr instead.
func DerefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ParseYouTubeUploadDate parses yt-dlp's upload_date YYYYMMDD format.
func ParseYouTubeUploadDate(dateStr string) (time.Time, error) {
	if len(dateStr) >= 8 {
		return time.Parse("20060102", dateStr[:8])
	}
	return time.Parse("20060102", dateStr)
}
