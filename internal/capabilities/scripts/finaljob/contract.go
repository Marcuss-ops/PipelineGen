package finaljob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

// BuildEnvelope decodes the producer's PREPARE and FINALIZE payloads into the
// versioned contract, parses the certified render plan and validates that all
// three representations describe the same handoff.
func BuildEnvelope(runID string, preparePayload, finalizePayload any) (EnvelopeV1, error) {
	var prepare PrepareV1
	if err := DecodeDTO(preparePayload, &prepare); err != nil {
		return EnvelopeV1{}, fmt.Errorf("final_job PREPARE DTO: %w", err)
	}
	var finalize FinalizeV1
	if err := DecodeDTO(finalizePayload, &finalize); err != nil {
		return EnvelopeV1{}, fmt.Errorf("final_job FINALIZE DTO: %w", err)
	}
	var plan CompiledPlanV2
	decoder := json.NewDecoder(strings.NewReader(prepare.CompiledRenderPlanJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return EnvelopeV1{}, fmt.Errorf("final_job compiled plan DTO: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return EnvelopeV1{}, fmt.Errorf("final_job compiled plan DTO: trailing JSON data")
	}
	envelope := EnvelopeV1{
		SchemaVersion: EnvelopeSchemaV1,
		RunID:         runID,
		Prepare:       prepare,
		Finalize:      finalize,
		Plan:          plan,
	}
	if err := envelope.Validate(); err != nil {
		return EnvelopeV1{}, err
	}
	return envelope, nil
}

// EnvelopeV1 is PipelineGen's typed model of one remote final-job
// handoff. SchemaVersion versions the producer contract; it is not emitted as
// a Master field because the deployed Master has separate PREPARE/FINALIZE
// request schemas.
type EnvelopeV1 struct {
	SchemaVersion string
	RunID         string
	Prepare       PrepareV1
	Finalize      FinalizeV1
	Plan          CompiledPlanV2
}

const EnvelopeSchemaV1 = "pipelinegen.final-job.v1"

type PrepareV1 struct {
	IdempotencyKey           string `json:"idempotency_key"`
	JobType                  string `json:"job_type"`
	RenderOnly               bool   `json:"render_only"`
	VideoName                string `json:"video_name"`
	ScriptText               string `json:"script_text"`
	CompiledRenderPlanJSON   string `json:"compiled_render_plan_json"`
	CompiledRenderPlanSHA256 string `json:"compiled_render_plan_sha256"`
	PlacementPinWorkerID     string `json:"placement_pin_worker_id,omitempty"`
}

type FinalizeV1 struct {
	IdempotencyKey string           `json:"idempotency_key"`
	RuntimeAssets  []RuntimeAssetV1 `json:"runtime_assets"`
	RuntimePayload RuntimePayloadV1 `json:"runtime_payload"`
}

// RuntimeAssetV1 types the immutable asset identity separately from
// its transport locator. Additional preserves forward-compatible Master
// metadata while the stable identity fields remain statically checked.
type RuntimeAssetV1 struct {
	AssetID     string
	SHA256      string
	SizeBytes   int64
	Kind        string
	Role        string
	URL         string
	SourceURI   string
	DriveFileID string
	Additional  map[string]json.RawMessage
}

type RuntimePayloadV1 struct {
	VideoName       string          `json:"video_name"`
	ScriptText      string          `json:"script_text"`
	Overlays        json.RawMessage `json:"overlays"`
	OverlayManifest json.RawMessage `json:"overlay_manifest,omitempty"`
	RuntimeAudio    *RuntimeAudioV1 `json:"runtime_audio,omitempty"`
}

type RuntimeAudioV1 struct {
	VoiceoverAssetID         string  `json:"voiceover_asset_id"`
	VoiceoverVolume          float64 `json:"voiceover_volume"`
	VoiceoverDurationSeconds float64 `json:"voiceover_duration_seconds"`
}

type CompiledPlanV2 struct {
	PlanVersion      int              `json:"plan_version"`
	TimelineRevision int64            `json:"timeline_revision"`
	TimelineSHA256   string           `json:"timeline_sha256"`
	DurationUS       int64            `json:"duration_us"`
	Output           PlanOutput       `json:"output"`
	FinalAudio       PlanAudio        `json:"final_audio"`
	VideoTracks      []PlanVideoTrack `json:"video_tracks"`
	Assets           []PlanAsset      `json:"assets"`
}

type PlanOutput struct {
	Container    string `json:"container"`
	VideoCodec   string `json:"video_codec"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	FPSNum       int    `json:"fps_num"`
	FPSDen       int    `json:"fps_den"`
	PixelFormat  string `json:"pixel_format,omitempty"`
	ProfileID    string `json:"profile_id,omitempty"`
	CodecProfile string `json:"codec_profile,omitempty"`
	CodecLevel   string `json:"codec_level,omitempty"`
	GOPSize      int    `json:"gop_size,omitempty"`
	BFrames      int    `json:"b_frames"`
	ClosedGOP    bool   `json:"closed_gop,omitempty"`
	TimeBaseNum  int    `json:"time_base_num,omitempty"`
	TimeBaseDen  int    `json:"time_base_den,omitempty"`
}

type PlanAudio struct {
	Mode             string `json:"mode"`
	AssetID          string `json:"asset_id"`
	SHA256           string `json:"sha256"`
	SizeBytes        int64  `json:"size_bytes"`
	Codec            string `json:"codec"`
	SampleRateHz     int    `json:"sample_rate_hz"`
	Channels         int    `json:"channels"`
	DurationUS       int64  `json:"duration_us"`
	TimelineRevision int64  `json:"timeline_revision"`
	TimelineSHA256   string `json:"timeline_sha256"`
}

type PlanVideoTrack struct {
	TrackID  string        `json:"track_id"`
	Segments []PlanSegment `json:"segments"`
}

type PlanSegment struct {
	SegmentID          string `json:"segment_id"`
	AssetID            string `json:"asset_id"`
	SHA256             string `json:"sha256"`
	TimelineStartFrame int64  `json:"timeline_start_frame"`
	FrameCount         int64  `json:"frame_count"`
	SourceInUS         int64  `json:"source_in_us"`
	SourceDurationUS   int64  `json:"source_duration_us"`
}

type PlanAsset struct {
	AssetID            string `json:"asset_id"`
	AssetKey           string `json:"asset_key,omitempty"`
	SHA256             string `json:"sha256"`
	SizeBytes          int64  `json:"size_bytes"`
	Kind               string `json:"kind"`
	MIME               string `json:"mime,omitempty"`
	DurationUS         int64  `json:"duration_us,omitempty"`
	Width              int    `json:"width,omitempty"`
	Height             int    `json:"height,omitempty"`
	ProfileID          string `json:"profile_id,omitempty"`
	FrameCount         int64  `json:"frame_count,omitempty"`
	TimelineRevision   int64  `json:"timeline_revision,omitempty"`
	TimelineSHA256     string `json:"timeline_sha256,omitempty"`
	TimelineStartFrame int64  `json:"timeline_start_frame,omitempty"`
	FirstFrameKeyframe bool   `json:"first_frame_keyframe,omitempty"`
	ClosedGOP          bool   `json:"closed_gop,omitempty"`
}

func DecodeDTO(source any, target any) error {
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON data")
	}
	return nil
}

func (e EnvelopeV1) Validate() error {
	if e.SchemaVersion != EnvelopeSchemaV1 {
		return fmt.Errorf("final_job schema_version %q is unsupported", e.SchemaVersion)
	}
	if e.RunID == "" || e.Prepare.IdempotencyKey == "" || e.Finalize.IdempotencyKey == "" {
		return fmt.Errorf("final_job run_id and both idempotency keys are required")
	}
	if e.Prepare.JobType != "video.render.v2" || !e.Prepare.RenderOnly {
		return fmt.Errorf("final_job PREPARE must declare video.render.v2 render_only")
	}
	if e.Prepare.VideoName == "" || e.Prepare.CompiledRenderPlanJSON == "" || e.Prepare.CompiledRenderPlanSHA256 == "" {
		return fmt.Errorf("final_job PREPARE requires video_name and a certified compiled plan")
	}
	if e.Plan.PlanVersion != 2 || e.Plan.DurationUS <= 0 || e.Plan.Output.Width <= 0 || e.Plan.Output.Height <= 0 ||
		e.Plan.Output.FPSNum <= 0 || e.Plan.Output.FPSDen <= 0 || e.Plan.FinalAudio.AssetID == "" || e.Plan.FinalAudio.DurationUS <= 0 {
		return fmt.Errorf("final_job compiled plan is missing timeline, output, or audio certification")
	}
	planBytes, err := json.Marshal(e.Plan)
	if err != nil {
		return fmt.Errorf("final_job encode compiled plan: %w", err)
	}
	if string(planBytes) != e.Prepare.CompiledRenderPlanJSON {
		return fmt.Errorf("final_job compiled plan DTO differs from PREPARE bytes")
	}
	if digest.SHA256String(e.Prepare.CompiledRenderPlanJSON) != e.Prepare.CompiledRenderPlanSHA256 {
		return fmt.Errorf("final_job compiled plan sha256 does not match plan bytes")
	}
	if len(e.Finalize.RuntimeAssets) == 0 {
		return fmt.Errorf("final_job FINALIZE requires runtime assets")
	}
	for i, asset := range e.Finalize.RuntimeAssets {
		if strings.TrimSpace(asset.AssetID) == "" || !digest.IsCanonicalSHA256(strings.TrimSpace(asset.SHA256)) || asset.SizeBytes <= 0 {
			return fmt.Errorf("final_job runtime asset %d lacks immutable identity, sha256, or size", i)
		}
	}
	for i, asset := range e.Plan.Assets {
		if strings.TrimSpace(asset.AssetID) == "" || !digest.IsCanonicalSHA256(strings.TrimSpace(asset.SHA256)) || asset.SizeBytes <= 0 {
			return fmt.Errorf("final_job compiled plan asset %d lacks immutable identity, sha256, or size", i)
		}
	}
	if err := ValidatePlanBindingRules(e.Plan); err != nil {
		return fmt.Errorf("final_job compiled plan binding: %w", err)
	}
	return nil
}

// MasterPayloads is the single adapter from the versioned producer DTO to the
// deployed split PREPARE/FINALIZE map contract. Nested plan/certification and
// asset fields are re-encoded from typed values, never edited as map entries.
func (e EnvelopeV1) MasterPayloads() (map[string]any, map[string]any, error) {
	if err := e.Validate(); err != nil {
		return nil, nil, err
	}
	prepareRaw, err := json.Marshal(e.Prepare)
	if err != nil {
		return nil, nil, fmt.Errorf("encode final_job PREPARE: %w", err)
	}
	finalizeRaw, err := json.Marshal(e.Finalize)
	if err != nil {
		return nil, nil, fmt.Errorf("encode final_job FINALIZE: %w", err)
	}
	prepare, err := decodeMasterPayload(prepareRaw)
	if err != nil {
		return nil, nil, err
	}
	finalize, err := decodeMasterPayload(finalizeRaw)
	if err != nil {
		return nil, nil, err
	}
	return prepare, finalize, nil
}

func decodeMasterPayload(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, fmt.Errorf("decode final_job Master payload: %w", err)
	}
	return result, nil
}

func (a RuntimeAssetV1) MarshalJSON() ([]byte, error) {
	values := make(map[string]json.RawMessage, len(a.Additional)+8)
	for key, raw := range a.Additional {
		values[key] = raw
	}
	put := func(key string, value any, omit bool) error {
		if omit {
			return nil
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		values[key] = raw
		return nil
	}
	for _, field := range []struct {
		key   string
		value any
		omit  bool
	}{
		{"asset_id", a.AssetID, a.AssetID == ""}, {"sha256", a.SHA256, a.SHA256 == ""},
		{"size_bytes", a.SizeBytes, a.SizeBytes == 0}, {"kind", a.Kind, a.Kind == ""},
		{"role", a.Role, a.Role == ""}, {"url", a.URL, a.URL == ""},
		{"source_uri", a.SourceURI, a.SourceURI == ""}, {"drive_file_id", a.DriveFileID, a.DriveFileID == ""},
	} {
		if err := put(field.key, field.value, field.omit); err != nil {
			return nil, err
		}
	}
	return json.Marshal(values)
}

func (a *RuntimeAssetV1) UnmarshalJSON(raw []byte) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return err
	}
	decode := func(key string, dest any) error {
		if value, ok := values[key]; ok {
			if err := json.Unmarshal(value, dest); err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			delete(values, key)
		}
		return nil
	}
	for _, field := range []struct {
		key  string
		dest any
	}{
		{"asset_id", &a.AssetID}, {"sha256", &a.SHA256}, {"size_bytes", &a.SizeBytes},
		{"kind", &a.Kind}, {"role", &a.Role}, {"url", &a.URL},
		{"source_uri", &a.SourceURI}, {"drive_file_id", &a.DriveFileID},
	} {
		if err := decode(field.key, field.dest); err != nil {
			return err
		}
	}
	a.Additional = values
	return nil
}
