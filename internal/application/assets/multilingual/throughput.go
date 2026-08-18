package multilingual

// throughput.go — Real-Time Factor and batch throughput for the benchmark
// report. These are DERIVED numbers (never stored as first-class columns):
// RTF normalizes a media operation's cost against the media duration it
// processed, and throughput normalizes a batch's wall time against the number
// of clips and media-minutes it produced — so clips of different durations are
// comparable.

// RTF returns the Real-Time Factor (processing time / media duration).
// < 1 means faster than realtime (e.g. 15s render of a 60s clip → 0.25, i.e.
// ~4× realtime); > 1 means slower than realtime. Returns 0 when the media
// duration is zero or negative.
func RTF(processingMS, mediaDurationMS int64) float64 {
	if mediaDurationMS <= 0 {
		return 0
	}
	return float64(processingMS) / float64(mediaDurationMS)
}

// Throughput is the batch-level benchmark projection: how many clips and how
// many media-minutes the job produced per minute of wall time, plus the
// aggregate render RTF (total render work / total media duration).
type Throughput struct {
	// ClipsPerMinute is completed clips / wall minutes.
	ClipsPerMinute float64 `json:"clips_per_minute"`
	// MediaMinutesPerMinute is total media-minutes / wall minutes. A value of
	// 1 means the pipeline sustains 1× realtime aggregated across the batch.
	MediaMinutesPerMinute float64 `json:"media_minutes_per_minute"`
	// RenderRTF is the aggregate render Real-Time Factor.
	RenderRTF float64 `json:"render_rtf"`
	// RenderWorkMS is the summed render work across all clips (≠ wall).
	RenderWorkMS int64 `json:"render_work_ms"`
	// MediaDurationMS is one clip's source duration (used for the aggregate).
	MediaDurationMS int64 `json:"media_duration_ms"`
}

// ComputeThroughput derives the batch throughput from clip count, per-clip
// media duration, wall time and summed render work. Zero/negative wall time or
// duration yields a zero-value Throughput (no division by zero).
func ComputeThroughput(clipCount int, mediaDurationMS int64, wallMS int64, renderWorkMS int64) Throughput {
	t := Throughput{RenderWorkMS: renderWorkMS, MediaDurationMS: mediaDurationMS}
	if wallMS <= 0 || clipCount <= 0 {
		return t
	}
	wallMinutes := float64(wallMS) / 60000.0
	if wallMinutes > 0 {
		t.ClipsPerMinute = float64(clipCount) / wallMinutes
	}
	totalMediaMinutes := float64(clipCount) * float64(mediaDurationMS) / 60000.0
	if wallMinutes > 0 {
		t.MediaMinutesPerMinute = totalMediaMinutes / wallMinutes
	}
	totalMediaMS := int64(clipCount) * mediaDurationMS
	if totalMediaMS > 0 {
		t.RenderRTF = float64(renderWorkMS) / float64(totalMediaMS)
	}
	return t
}
