package finaljob

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
)

func TestRuntimeAssetV1RoundTripPreservesAdditionalFields(t *testing.T) {
	source := map[string]any{
		"asset_id": "asset-1", "sha256": strings.Repeat("a", 64), "size_bytes": int64(2048),
		"kind": "video", "role": "clip", "source_uri": "https://objects.example/asset-1",
		"duration_ms": int64(1000), "copy_eligible": true, "profile_id": "VELOX_ASSEMBLY_READY_V1",
	}
	asset, err := RuntimeAssetFromMap(source)
	if err != nil {
		t.Fatalf("RuntimeAssetFromMap: %v", err)
	}
	roundTrip, err := asset.Map()
	if err != nil {
		t.Fatalf("Map: %v", err)
	}
	for key, want := range source {
		if got := roundTrip[key]; !reflect.DeepEqual(got, want) {
			t.Fatalf("round-trip %q = %#v, want %#v", key, got, want)
		}
	}
	if got := Int64(roundTrip["duration_ms"]); got != 1000 {
		t.Fatalf("duration_ms = %d, want 1000", got)
	}
	if !Bool(roundTrip["copy_eligible"]) {
		t.Fatal("copy_eligible was not preserved as a bool")
	}
}

func TestEnvelopeV1RoundTripKeepsPlanBytesAndHash(t *testing.T) {
	videoSHA := strings.Repeat("a", 64)
	audioSHA := strings.Repeat("b", 64)
	plan := CompiledPlanV2{
		PlanVersion: 2, TimelineRevision: 1, TimelineSHA256: strings.Repeat("c", 64), DurationUS: 1_000_000,
		Output: PlanOutput{
			Container: "mp4", VideoCodec: "h264", Width: 1920, Height: 1080,
			FPSNum: 24, FPSDen: 1, PixelFormat: "yuv420p",
		},
		FinalAudio: PlanAudio{
			Mode: "FINAL_AUDIO_COPY", AssetID: "asset-audio", SHA256: audioSHA, SizeBytes: 4096,
			Codec: "aac", SampleRateHz: 48000, Channels: 2, DurationUS: 1_000_000,
			TimelineRevision: 1, TimelineSHA256: strings.Repeat("c", 64),
		},
		VideoTracks: []PlanVideoTrack{{TrackID: "main-video", Segments: []PlanSegment{{
			SegmentID: "segment-0000", AssetID: "asset-video", SHA256: videoSHA,
			TimelineStartFrame: 0, FrameCount: 24, SourceInUS: 0, SourceDurationUS: 1_000_000,
		}}}},
		Assets: []PlanAsset{
			{AssetID: "asset-video", AssetKey: "sha256:" + videoSHA, SHA256: videoSHA, SizeBytes: 2048, Kind: "video", DurationUS: 1_000_000},
			{AssetID: "asset-audio", AssetKey: "sha256:" + audioSHA, SHA256: audioSHA, SizeBytes: 4096, Kind: "final_audio", DurationUS: 1_000_000},
		},
	}
	planBytes, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	planSHA := digest.SHA256String(string(planBytes))
	envelope := EnvelopeV1{
		SchemaVersion: EnvelopeSchemaV1,
		RunID:         "run-golden",
		Prepare: PrepareV1{
			IdempotencyKey: "run-golden:1", JobType: "video.render.v2", RenderOnly: true,
			VideoName: "golden", ScriptText: "A golden handoff.",
			CompiledRenderPlanJSON: string(planBytes), CompiledRenderPlanSHA256: planSHA,
		},
		Finalize: FinalizeV1{
			IdempotencyKey: "run-golden:1-finalize",
			RuntimeAssets: []RuntimeAssetV1{{
				AssetID: "asset-video", SHA256: videoSHA, SizeBytes: 2048,
				Kind: "video", Role: "clip", SourceURI: "https://objects.example/asset-video",
				Additional: map[string]json.RawMessage{
					"duration_ms": json.RawMessage("1000"),
				},
			}},
			RuntimePayload: RuntimePayloadV1{
				VideoName: "golden", ScriptText: "A golden handoff.", Overlays: []OverlayRowV1{},
			},
		},
		Plan: plan,
	}
	if err := envelope.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	prepare, finalize, err := envelope.MasterPayloads()
	if err != nil {
		t.Fatalf("MasterPayloads: %v", err)
	}
	roundTrip, err := BuildEnvelope(envelope.RunID, prepare, finalize)
	if err != nil {
		t.Fatalf("BuildEnvelope: %v", err)
	}
	if !reflect.DeepEqual(envelope, roundTrip) {
		t.Fatalf("typed envelope changed across the wire\nwant: %#v\ngot:  %#v", envelope, roundTrip)
	}
	if roundTrip.Prepare.CompiledRenderPlanJSON != string(planBytes) {
		t.Fatalf("canonical plan bytes changed across the wire")
	}
	if got := digest.SHA256String(roundTrip.Prepare.CompiledRenderPlanJSON); got != planSHA {
		t.Fatalf("plan hash = %s, want %s", got, planSHA)
	}
	if !reflect.DeepEqual(roundTrip.Plan, plan) {
		t.Fatalf("typed plan changed across the wire")
	}
}
