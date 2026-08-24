package defaults

// YouTubeConfig is the canonical SSOT for YouTube-channel monitor
// defaults and the classifier fallback category.
//
// Pre-fix scattered literals this SSOT replaces (June 2026, Step 4
// PR4 — DRIFT-DEFAULTS-YOUTUBE):
//
//   - "general" string at five+ call sites (the *fallback scalar*
//     is centralised here; the *categories list* literal at e.g.
//     classifier.go:168 is intentionally NOT covered by this SSOT
//     — a future "rename general" change to the fallback does not
//     require touching the list, and vice versa. The per-site
//     scalar defaults at classifier.go:53 / 101 ARE covered by
//     this SSOT):
//     internal/infrastructure/ai/classifier/classifier.go
//     (FallbackCategory default, default categories list)
//     internal/application/youtube/usecase/callbacks.go
//     (FallbackCategory, category list)
//     internal/application/youtube/adapters/segment_processor.go
//     (group := "general")
//     internal/application/assets/videomuscles/youtube_pipeline.go
//     (filepath.Join(... "media", "clips", "general", videoID))
//     internal/capabilities/assets/providers/artlist/run_orchestrator_stages.go
//     (same path-join pattern under "media", "artlist", "general", genID).
//   - channels.Default policy struct (internal/application/channels/
//     service.go::Default) — MaxClipDuration=60, Priority=2,
//     MaxSegments=2, MaxVideosPerRun=3, MinSemanticScore=60,
//     CheckInterval="7d", PlaylistEnd=-1.
//
// Every consumer MUST read from DefaultYouTubeConfig() rather than
// re-implementing these literals inline. A future "rename general
// to default" or "tune MaxSegments to 3" change is then a one-line
// edit; pre-fix it required grep across 5+ files and risk of
// forgetting one path-join call site.
//
// Shape is intentionally tiny (8 leaf fields) to keep pkg/defaults
// leaf-only: zero imports from internal/, only consumed by callers
// crossing the infra→application seam.
type YouTubeConfig struct {
	// FallbackCategory is the category applied when classification
	// fails. Legacy inlined value: "general". Used both as the
	// fallback value and as a fixed path segment under
	// media/{clips|artlist}/.
	FallbackCategory string

	// MaxSegments is the per-video cap on extracted segments.
	// Legacy inlined value: 2 (from channels.Default).
	MaxSegments int

	// MaxClipDuration is the per-segment cap in seconds. Legacy
	// inlined value: 60 (from channels.Default).
	MaxClipDuration int

	// MinSemanticScore is the 0-100 confidence floor for accepting
	// a semantic-match hit. Legacy inlined value: 60 (from
	// channels.Default).
	MinSemanticScore int

	// MaxVideosPerRun is the per-channel cap on videos to process
	// in a single monitor run. Legacy inlined value: 3 (from
	// channels.Default).
	MaxVideosPerRun int

	// CheckInterval is the duration string between monitor runs
	// per channel. Legacy inlined value: "7d" (from
	// channels.Default).
	CheckInterval string

	// Priority is the channel-monitor priority bucket. Legacy
	// inlined value: 2 (from channels.Default).
	Priority int

	// PlaylistEnd is the per-channel playlist end hint; -1 means
	// "use the global config" (legacy SQL sentinel, preserved here
	// as the canonical owner of the magic number).
	PlaylistEnd int
}

// DefaultYouTubeConfig returns the canonical DRIFT-DEFAULTS-YOUTUBE
// SSOT. Treat the returned value as immutable per consumer site (no
// process-global mutation — copy and adjust locally if needed).
func DefaultYouTubeConfig() YouTubeConfig {
	return YouTubeConfig{
		FallbackCategory: "general",
		MaxSegments:      2,
		MaxClipDuration:  60,
		MinSemanticScore: 60,
		MaxVideosPerRun:  3,
		CheckInterval:    "7d",
		Priority:         2,
		PlaylistEnd:      -1,
	}
}
