package videocreate

import (
	"encoding/json"
	"errors"
	"testing"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	voicesvc "github.com/Marcuss-ops/PipelineGen/internal/capabilities/voiceover/service"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	kernelmedia "github.com/Marcuss-ops/PipelineGen/internal/kernel/media"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
)

// ── Child payload contracts (godlike/06 SSOT) ─────────────────────────
//
// Every builder must produce the child family's OWN canonical request
// type — the exact struct the registered handler decodes from
// job.Payload. These tests round-trip each payload through JSON into
// the HANDLER-SIDE type and run that type's own Validate, so a payload
// the real worker would reject breaks the build HERE instead of at
// runtime.

func TestScriptChildPayload_RealEnvelopeContract(t *testing.T) {
	req := appjobs.VideoCreatePayload{
		Topic: "Mike Tyson", DurationSeconds: 60,
		Language: "en", Voiceover: true, MediaSources: []string{"youtube"},
	}
	payload, err := ScriptChildPayload(req, "proj", "vid", "root:script")
	if err != nil {
		t.Fatalf("ScriptChildPayload: %v", err)
	}
	raw := mustRaw(payload)
	var env scriptpkg.GenerationEnvelopeV2
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("handler-side decode: %v", err)
	}
	if err := env.Validate(); err != nil {
		t.Fatalf("GenerationEnvelopeV2.Validate: %v (the real handler would reject this payload)", err)
	}
	if len(env.Items) != 1 {
		t.Fatalf("items = %d, want exactly 1", len(env.Items))
	}
	item := env.Items[0]
	if item.Source.Type != scriptpkg.SourceText || item.Source.Topic != "Mike Tyson" {
		t.Errorf("source = %+v, want text/topic source", item.Source)
	}
	if item.Output.VoiceoverEnabled != scriptpkg.ToggleEnabled {
		t.Errorf("voiceover_enabled = %q, want enabled", item.Output.VoiceoverEnabled)
	}
	if item.Audio.Mode != string(audio.AudioModeCombinedTimeline) {
		t.Errorf("audio.mode = %q, want %q", item.Audio.Mode, audio.AudioModeCombinedTimeline)
	}
	if item.ScriptParams.TargetWords < 40 {
		t.Errorf("target_words = %d, want >= 40", item.ScriptParams.TargetWords)
	}
}

func TestAcquireChildPayload_RealContracts(t *testing.T) {
	req := appjobs.VideoCreatePayload{
		Topic: "Mike Tyson", DurationSeconds: 60,
		Language: "en", MediaSources: []string{"youtube", "stock"},
	}

	yt, _, err := AcquireChildPayload(MediaCandidate{Source: "youtube", SourceURL: "https://youtu.be/abc", Title: "clip"}, req, "proj", "vid", 15)
	if err != nil {
		t.Fatalf("AcquireChildPayload(youtube): %v", err)
	}
	ytRaw := mustRaw(yt)
	var extract youtubetypes.ExtractRequest
	if err := json.Unmarshal(ytRaw, &extract); err != nil {
		t.Fatalf("youtube handler-side decode: %v", err)
	}
	if extract.URL != "https://youtu.be/abc" {
		t.Errorf("url = %q", extract.URL)
	}
	if extract.Selection == nil || extract.Selection.Mode != string(youtubetypes.SegmentSelectionModeImportant) {
		t.Errorf("selection = %+v, want important mode", extract.Selection)
	}
	if extract.RequireTranscriptReady == nil || !*extract.RequireTranscriptReady {
		t.Errorf("require_transcript_ready must be hard-set true")
	}

	st, family, err := AcquireChildPayload(MediaCandidate{Source: "stock", Title: "boxing gloves"}, req, "proj", "vid", 15)
	if err != nil {
		t.Fatalf("AcquireChildPayload(stock): %v", err)
	}
	if family != "stock" {
		t.Errorf("family = %q, want stock", family)
	}
	var run stockpipeline.StockRunPayload
	if err := json.Unmarshal(mustRaw(st), &run); err != nil {
		t.Fatalf("stock handler-side decode: %v", err)
	}
	if len(run.SearchQueries) == 0 || run.SearchQueries[0] != "boxing gloves" {
		t.Errorf("search_queries = %v, want the candidate title", run.SearchQueries)
	}
	if run.TotalMinutes < 1 || run.ClipDurationSeconds != 15 {
		t.Errorf("budget: total_minutes=%d clip_duration_seconds=%d", run.TotalMinutes, run.ClipDurationSeconds)
	}
	if run.Subfolder != "proj" || run.FolderName != "vid" {
		t.Errorf("destination: subfolder=%q folder_name=%q", run.Subfolder, run.FolderName)
	}
}

// TestAcquireChildPayload_ExplicitWindow pins the registry-clip path: a
// youtube candidate with a KNOWN window becomes an explicit-segment
// ExtractRequest (the production wire shape — watch URL + HH:MM:SS window),
// never the LLM re-derivation selection.
func TestAcquireChildPayload_ExplicitWindow(t *testing.T) {
	cand := MediaCandidate{
		AssetID: "yt_YrOKvXhFEuw_42_57_v1", Source: "youtube",
		SourceVideoID: "YrOKvXhFEuw", StartSec: 42, EndSec: 57,
		Title: "Hulkamania, Real American e catchphrase iconiche",
	}
	payload, family, err := AcquireChildPayload(cand, appjobs.VideoCreatePayload{
		Topic: "Hulk Hogan", Language: "it", MediaSources: []string{"youtube"},
	}, "proj", "video-name", 15)
	if err != nil {
		t.Fatalf("AcquireChildPayload: %v", err)
	}
	if family != "youtube" {
		t.Fatalf("family = %q, want youtube", family)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var req youtubetypes.ExtractRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("REAL handler decode: %v", err)
	}
	if req.URL != "https://www.youtube.com/watch?v=YrOKvXhFEuw" {
		t.Errorf("url = %q", req.URL)
	}
	if req.Selection != nil {
		t.Errorf("selection = %+v, want nil (explicit segments)", req.Selection)
	}
	if len(req.Segments) != 1 || req.Segments[0].Start != "00:00:42" || req.Segments[0].End != "00:00:57" {
		t.Errorf("segments = %+v, want one 00:00:42-00:00:57 window", req.Segments)
	}
	if req.RequireTranscriptReady == nil || !*req.RequireTranscriptReady {
		t.Error("require_transcript_ready must stay true (07_render transcript.mode=reuse)")
	}
}

func TestVoiceoverChildPayload_RealCommand(t *testing.T) {
	cmd, err := VoiceoverChildPayload([]string{"Mike Tyson training", "championship rounds", ""}, "en", "proj")
	if err != nil {
		t.Fatalf("VoiceoverChildPayload: %v", err)
	}
	raw := mustRaw(cmd)
	var decoded voicesvc.GenerateVoiceoversCommand
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("voiceover handler-side decode: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("GenerateVoiceoversCommand.Validate: %v (the real handler would reject this payload)", err)
	}
	if len(decoded.Items) != 2 {
		t.Fatalf("items = %d, want 2 (empty text dropped)", len(decoded.Items))
	}
	for i, item := range decoded.Items {
		if !item.Required {
			t.Errorf("items[%d].Required = false, want true", i)
		}
		if string(item.Language) != "en" {
			t.Errorf("items[%d].language = %q, want en", i, item.Language)
		}
	}
}

func TestRenderChildPayload_RealRequest(t *testing.T) {
	payload, err := RenderChildPayload("asset-1", "en", "16:9", true)
	if err != nil {
		t.Fatalf("RenderChildPayload: %v", err)
	}
	raw := mustRaw(payload)
	var req cliprender.RenderRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		t.Fatalf("clip.render handler-side decode: %v", err)
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("RenderRequest.Validate: %v (the real worker would reject this payload)", err)
	}
	if req.SourceAssetID != "asset-1" {
		t.Errorf("source_asset_id = %q", req.SourceAssetID)
	}
	if req.Output == nil || req.Output.Contract != cliprender.OutputContractVeloxAssemblyReadyV1 {
		t.Errorf("output.contract = %+v, want %s (the assembly-ready contract)", req.Output, cliprender.OutputContractVeloxAssemblyReadyV1)
	}
	if req.Output.Width != 1920 || req.Output.Height != 1080 {
		t.Errorf("output geometry = %dx%d, want 1920x1080", req.Output.Width, req.Output.Height)
	}
	if _, err := RenderChildPayload("asset-1", "en", "9:16", false); !errors.Is(err, ErrInvalidPayload) {
		t.Errorf("vertical aspect must fail closed, got %v", err)
	}
}

// ── Child result contracts ────────────────────────────────────────────
//
// Each decoder is pinned against the family's REAL result wire shape
// (the canned fixtures in fakes_test.go are built from the handler-side
// types / the handler's own projection), so a child-side result change
// breaks the build here instead of silently emptying a stage.

func TestDecodeScriptChildResult_DurableWire(t *testing.T) {
	res, err := decodeScriptChildResult(cannedScriptResult("job_1", "abcdef0123456789"))
	if err != nil {
		t.Fatalf("decodeScriptChildResult(durable): %v", err)
	}
	if res.ScriptAssetID == "" {
		t.Error("script_asset_id empty (manifest script_json artifact must resolve)")
	}
	if len(res.Scenes) != 2 || len(res.TextSegments) != 2 {
		t.Errorf("scenes/text = %v/%v, want 2/2", res.Scenes, res.TextSegments)
	}
	if res.TextSegments[0] != "Mike Tyson training" {
		t.Errorf("text_segments[0] = %q", res.TextSegments[0])
	}
	if len(res.Voiceovers) != 2 || res.Voiceovers[0].LocalPath == "" || res.Voiceovers[0].DurationMS != 3500 {
		t.Errorf("voiceovers = %+v, want 2 materialized refs", res.Voiceovers)
	}
	if len(res.AudioPlan) == 0 {
		t.Error("audio_plan empty (the durable result carries the compiled plan)")
	}
}

func TestDecodeScriptChildResult_EnvelopeWire(t *testing.T) {
	env := scriptpkg.GenerationEnvelopeResult{
		Version: scriptpkg.EnvelopeVersion,
		OK:      true,
		Items: []scriptpkg.GenerationEnvelopeItem{{
			ItemID: "root:script",
			Result: &scriptpkg.GenerationResult{
				Output: scriptpkg.ScriptOutput{
					Text: "Mike Tyson training",
					SpecScene: scriptpkg.SpecSceneOutput{
						Version: 1,
						Scenes:  []scriptpkg.SpecScene{{ID: "scene-001", Index: 0, Text: "Mike Tyson training"}},
					},
				},
			},
		}},
	}
	res, err := decodeScriptChildResult(mustRaw(env))
	if err != nil {
		t.Fatalf("decodeScriptChildResult(envelope): %v", err)
	}
	if len(res.Scenes) != 1 || res.Scenes[0] != "scene-001" {
		t.Errorf("scenes = %v, want [scene-001]", res.Scenes)
	}
}

func TestDecodeAcquireChildResult_RealWireShapes(t *testing.T) {
	yt, err := decodeAcquireChildResult(appjobs.TypeYouTubeClipExtract, cannedYouTubeResult(mustRaw(youtubetypes.ExtractRequest{URL: "https://youtu.be/abc"}), "abcdef0123456789"))
	if err != nil {
		t.Fatalf("youtube decode: %v", err)
	}
	if yt.AssetID == "" || yt.Source != "youtube" || yt.LocalPath == "" || yt.ContentSHA == "" {
		t.Errorf("youtube result = %+v, want asset/source/local materialization", yt)
	}
	st, err := decodeAcquireChildResult(appjobs.TypeMediaStock, cannedStockResult("job_2", "abcdef0123456789"))
	if err != nil {
		t.Fatalf("stock decode: %v", err)
	}
	if st.AssetID != "stock:abcdef012345" || st.LocalPath == "" || st.DurationMS != 5000 {
		t.Errorf("stock result = %+v", st)
	}
}

func TestDecodeVoiceoverItemResult_RealWireShape(t *testing.T) {
	ref, err := decodeVoiceoverItemResult(cannedVoiceoverItemResult("job_vo", "corr", "en", "root:voiceover:item:001"))
	if err != nil {
		t.Fatalf("voiceover item decode: %v", err)
	}
	if ref.LocalPath == "" || ref.DriveFileID == "" || ref.DurationMS != 3500 || ref.Language != "en" {
		t.Errorf("voiceover ref = %+v", ref)
	}
}

func TestDecodeRenderChildResult_CopyCertificationDerived(t *testing.T) {
	res, err := decodeRenderChildResult(cannedRenderResult("job_render", mustRaw(cliprender.RenderRequest{SourceAssetID: "asset-1"}), "abcdef0123456789"))
	if err != nil {
		t.Fatalf("render decode: %v", err)
	}
	if res.ContractID != kernelmedia.AssemblyMediaContractID {
		t.Fatalf("contract_id = %q, want %s", res.ContractID, kernelmedia.AssemblyMediaContractID)
	}
	seg, err := res.assembleSegment()
	if err != nil {
		t.Fatalf("assembleSegment: %v", err)
	}
	if !seg.CopyCertified || seg.LocalPath == "" || seg.Contract.Width != 1920 {
		t.Errorf("segment = %+v, want copy-certified with contract facts", seg)
	}
	cert, err := CopyCertificationFor([]AssembleSegment{seg, seg})
	if err != nil {
		t.Fatalf("CopyCertificationFor: %v", err)
	}
	if err := cert.Validate(); err != nil {
		t.Fatalf("cert.Validate (the Rust gate would reject this): %v", err)
	}
	if cert.ContractID != kernelmedia.AssemblyMediaContractID || cert.Width != 1920 || cert.FPSNum != 24 {
		t.Errorf("cert = %+v", cert)
	}

	// A segment whose render contract is NOT the assembly-ready contract
	// carries no copy certification and must fail closed.
	off := res
	off.ContractID = "SOMETHING_ELSE"
	if _, err := off.assembleSegment(); err == nil {
		t.Error("assembleSegment accepted a non assembly-ready contract")
	}
}

func TestCopyCertificationFor_RejectsMixedContracts(t *testing.T) {
	res, err := decodeRenderChildResult(cannedRenderResult("job_render", mustRaw(cliprender.RenderRequest{SourceAssetID: "asset-1"}), "abcdef0123456789"))
	if err != nil {
		t.Fatalf("render decode: %v", err)
	}
	seg, err := res.assembleSegment()
	if err != nil {
		t.Fatalf("assembleSegment: %v", err)
	}
	other := seg
	other.Contract = RenderContractFacts{Width: 1280, Height: 720, FPSNum: 24, FPSDen: 1, VideoCodec: "h264", AudioCodec: "aac", PixelFormat: "yuv420p"}
	if _, err := CopyCertificationFor([]AssembleSegment{seg, other}); err == nil {
		t.Error("CopyCertificationFor accepted mixed contract blocks (ASSEMBLY_INPUT_CONTRACT_MISMATCH)")
	}
	uncertified := seg
	uncertified.ContractID = ""
	if _, err := CopyCertificationFor([]AssembleSegment{uncertified}); err == nil {
		t.Error("CopyCertificationFor accepted a contract-less segment")
	}
	if _, err := CopyCertificationFor(nil); err == nil {
		t.Error("CopyCertificationFor accepted an empty batch")
	}
}

// scriptgen is referenced so the durable wire fixture stays pinned to
// the handler-side capability result type (unused-import guard).
var _ = scriptgen.GenerateResult{}
