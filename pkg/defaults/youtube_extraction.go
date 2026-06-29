package defaults

// YouTubeExtractionConfig is the canonical configuration for YouTube
// extraction policy values that previously lived as scattered magic
// numbers across the YouTube adapter, the channels service, and the
// manifest manager. It is the single source of truth for the values
// used in canonical YouTube asset extraction.
//
// HC-5 (June 2026): replaces the pre-HC-5 scattered literals:
//
//   - DefaultGroup: "general" (was a hard-coded magic string in the
//     YouTube extraction-pipeline; surfaced in multiple packages with
//     two different casings — HC-5 unifies the surface).
//   - MaxSegments: 2 (was a magic number in the channels/service.go
//     `Default.MaxSegments: 2` policy struct; HC-5 routes the read
//     through this SSOT so the canonical value lives in one place).
//   - clipfolder_youtube_* prefix family (was a magic-string family in
//     the manifest manager; HC-5 routes readers through this SSOT).
//
// Every consumer MUST call DefaultYouTubeExtractionConfig() rather than
// hard-coding any of these values; the anti-reintro gate is Check 43
// in scripts/ci-architectural-checks.sh.
type YouTubeExtractionConfig struct {
	// DefaultGroup is the fallback asset group assigned when an
	// extraction candidate has no explicit group tag. Default
	// "general" — matches the channels/service.go historical value.
	DefaultGroup string

	// MaxSegments is the per-track segment cap applied during
	// YouTube extraction. Default 2 — matches the channels policy.
	//
	// DRIFT clarification (June 2026, code-review): YouTubeExtractionConfig.MaxSegments
	// is distinct from YouTubeConfig.MaxSegments in pkg/defaults/youtube.go.
	// Both happen to share the value 2 today; the two fields govern DIFFERENT
	// capability surfaces (extraction pipeline vs. channel-monitor loop).
	// Future drift between the two is INTENDED, not an anti-pattern — each
	// SSOT owns its own number. Anti-reintro Check 43 scans for the literal
	// `MaxSegments := 2` only in this file's scope (production consumers must
	// consult DefaultYouTubeExtractionConfig().MaxSegments explicitly).
	MaxSegments int

	// ClipFolderPrefix is the string family prefix used for the
	// YouTube clip-folder naming convention. Default "clipfolder_youtube_".
	// Adjustments MUST land here so the prefix family is governed in
	// a single place.
	ClipFolderPrefix string
}

// DefaultYouTubeExtractionConfig returns the canonical HC-5
// YouTubeExtractionConfig SSOT.
func DefaultYouTubeExtractionConfig() YouTubeExtractionConfig {
	return YouTubeExtractionConfig{
		DefaultGroup:     "general",
		MaxSegments:      2,
		ClipFolderPrefix: "clipfolder_youtube_",
	}
}
