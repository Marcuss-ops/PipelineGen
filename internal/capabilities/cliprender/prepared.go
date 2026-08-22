package cliprender

// prepared.go owns the typed result of the parallel preparation phase:
// the resolved asset identities, the materialized local artifacts, the
// canonical transcript, the resolved output contract, and the wall-vs-work
// timing projection. All types are capability-owned so no port leaks a
// technology-specific type (Verdetto invariant, mirror of
// internal/capabilities/scripts/ports.go).
//
// The Preparer (preparer.go) fans out the phases concurrently and returns a
// *Prepared. The next step (ClipRenderPlanV1 compilation + single-pass Rust
// render) consumes every resolved value here; Rust never makes business
// selections.

// AssetRef is the resolved canonical asset identity handed to the
// materializer. It carries only the fields materialization needs — never a
// raw caller-supplied path (asset_id is the only identity the API accepts).
type AssetRef struct {
	AssetID     string
	MediaType   string
	LocalPath   string // registered local copy (may be absent)
	DriveFileID string // canonical Drive source (may be absent)
	LegacyFileMD5    string // registry-persisted hash (may be absent)
	DurationMS  int64
}

// MaterializedAsset is a local, content-addressed artifact ready for the
// render pass. LocalPath is the absolute on-disk file; SHA256 is the
// verified digest of the bytes at that path.
type MaterializedAsset struct {
	AssetID    string
	LocalPath  string
	SHA256     string
	SizeBytes  int64
	DurationMS int64
	// FromCache is true when the asset was already local (no download was
	// performed). Observability only — the render pass never branches on it.
	FromCache bool
}

// Cue is a single timed subtitle cue (millisecond precision), mirroring the
// canonical asset.TimedCue shape so the capability stays free of kernel/asset
// imports. The wiring adapter maps []asset.TimedCue → []Cue.
type Cue struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Text    string `json:"text"`
}

// TranscriptInput is the typed transcript-resolution request. Mode mirrors
// the request's transcript.mode (reuse | generate | reuse_or_generate).
type TranscriptInput struct {
	AssetID      string
	Language     string
	Mode         string
	Persist      bool
	SourceSHA256 string // digest of the materialized source (recorded as source_audio_sha256)
}

// TranscriptResult is the canonical transcript + timing. Reused is true when
// an existing READY canonical text track satisfied the request (no speech
// recognition ran).
type TranscriptResult struct {
	AssetID           string
	Language          string
	Text              string
	Cues              []Cue
	TextSHA256        string
	Reused            bool
	SourceAudioSHA256 string
	DurationMS        int64
	Confidence        *float64
	// StreamSourceType records the concrete generation source (whisper chain,
	// streaming PCM bridge, ...) so persistence can tag the canonical track.
	StreamSourceType string
}

// HasText reports whether the result carries usable transcript content.
func (t *TranscriptResult) HasText() bool {
	return t != nil && (t.Text != "" || len(t.Cues) > 0)
}

// ResolvedContract is the fully-resolved VeloxEditing output contract. The
// precise codec/pixel/timebase values are owned here (single canonical
// owner); the render pass and contract validator consume this verbatim.
type ResolvedContract struct {
	ContractID   string
	Container    string
	VideoCodec   string
	VideoProfile string
	PixelFormat  string
	Width        int
	Height       int
	FPSNum       int
	FPSDen       int
	AudioCodec   string
	SampleRate   int
	Channels     int
}

// PhaseTiming records one preparation phase. WorkMS is the phase's own
// accumulated duration; WallMS is the same value for a phase that ran
// isolated, and the Preparer's aggregate compares total wall vs accumulated
// work to expose the parallelism win.
//
// Notes are optional observability hints emitted by the phase (cache hits,
// downloaded bytes, probe facts). They flow through the same tracker so
// downstream logging can render them without re-running the phase.
type PhaseTiming struct {
	Phase  string         `json:"phase"`
	WallMS int64          `json:"wall_ms"`
	WorkMS int64          `json:"work_ms"`
	Notes  map[string]any `json:"notes,omitempty"`
}

// PreparationTimings is the canonical observability projection of the
// parallel preparation phase. TotalWorkMS is the sum of every phase's work;
// TotalWallMS is the elapsed wall time across the whole phase. When phases
// overlap, TotalWallMS < TotalWorkMS (the parallelism win is observable).
type PreparationTimings struct {
	TotalWallMS int64         `json:"total_wall_ms"`
	TotalWorkMS int64         `json:"total_work_ms"`
	Parallel    bool          `json:"parallel"`
	Phases      []PhaseTiming `json:"phases"`
}

// Prepared is the complete output of the parallel preparation phase. It is
// the typed handoff to the next step (ClipRenderPlanV1 compilation).
type Prepared struct {
	// RunID is the job identity scoped to this render attempt (used for the
	// Drive TMP run subfolder and deterministic artifact naming).
	RunID string `json:"run_id"`

	Source     *MaterializedAsset `json:"source"`
	Watermark  *MaterializedAsset `json:"watermark,omitempty"`
	Background *MaterializedAsset `json:"background,omitempty"`
	Transcript *TranscriptResult  `json:"transcript,omitempty"`
	Contract   *ResolvedContract  `json:"contract"`

	Timings PreparationTimings `json:"timings"`
}
