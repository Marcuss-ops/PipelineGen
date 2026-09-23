package videocreate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	cliprender "github.com/Marcuss-ops/PipelineGen/internal/capabilities/cliprender"
	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
	scriptgen "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts"
	youtubetypes "github.com/Marcuss-ops/PipelineGen/internal/capabilities/youtube/dto"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// ── fakeStore: the steps.Store contract in memory ─────────────────────
//
// Implements the documented semantics the workflow depends on:
// terminal-completion immutability (ErrStepAlreadyCompleted), idempotent
// MarkStarted per triple, and FirstNonCompleted over the LATEST row per
// (jobID, stepKey).
type fakeStore struct {
	mu   sync.Mutex
	seq  int64
	rows []*steps.StepState
}

func newFakeStore() *fakeStore { return &fakeStore{} }

func (s *fakeStore) MarkStarted(_ context.Context, key steps.StepKey) error {
	if err := key.Validated(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest := s.latestLocked(key.JobID, key.StepKey)
	if latest != nil {
		if latest.Status == steps.StatusCompleted {
			return steps.ErrStepAlreadyCompleted
		}
		if latest.Fingerprint == key.InputFingerprint {
			latest.Attempt++
			latest.Status = steps.StatusPending
			return nil
		}
	}
	s.seq++
	s.rows = append(s.rows, &steps.StepState{
		ID: s.seq, JobID: key.JobID, StepKey: key.StepKey,
		Fingerprint: key.InputFingerprint, Status: steps.StatusPending,
	})
	return nil
}

func (s *fakeStore) MarkCompleted(_ context.Context, key steps.StepKey, result, artifactRefs json.RawMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rowLocked(key)
	if row == nil {
		return steps.ErrStepNotFound
	}
	if row.Status == steps.StatusCompleted {
		return steps.ErrStepAlreadyCompleted
	}
	row.Status = steps.StatusCompleted
	row.Result = result
	row.ArtifactRefs = artifactRefs
	return nil
}

func (s *fakeStore) MarkFailed(_ context.Context, key steps.StepKey, errMessage string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.rowLocked(key)
	if row == nil {
		return steps.ErrStepNotFound
	}
	if row.Status == steps.StatusCompleted {
		return steps.ErrStepAlreadyCompleted
	}
	row.Status = steps.StatusFailed
	row.LastError = errMessage
	return nil
}

func (s *fakeStore) FirstNonCompleted(_ context.Context, jobID string) (*steps.StepState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, spec := range WorkflowSteps {
		latest := s.latestLocked(jobID, spec.StepKey)
		if latest == nil || latest.Status != steps.StatusCompleted {
			if latest == nil {
				return &steps.StepState{JobID: jobID, StepKey: spec.StepKey}, nil
			}
			return latest, nil
		}
	}
	return nil, nil
}

func (s *fakeStore) ListByJob(_ context.Context, jobID string) ([]steps.StepState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []steps.StepState
	for _, row := range s.rows {
		if row.JobID == jobID {
			out = append(out, *row)
		}
	}
	return out, nil
}

func (s *fakeStore) latestLocked(jobID, stepKey string) *steps.StepState {
	var latest *steps.StepState
	for _, row := range s.rows {
		if row.JobID == jobID && row.StepKey == stepKey {
			latest = row
		}
	}
	return latest
}

func (s *fakeStore) rowLocked(key steps.StepKey) *steps.StepState {
	for _, row := range s.rows {
		if row.JobID == key.JobID && row.StepKey == key.StepKey && row.Fingerprint == key.InputFingerprint {
			return row
		}
	}
	return nil
}

// ── fakeChildren: the ChildJobs contract in memory ────────────────────
//
// EnqueueChild is IDEMPOTENT on IdempotencyKey (the broker contract the
// §8 replay safety rests on): a duplicate returns the existing child id
// and bumps no work counter. Terminal results are canned per job type
// (the children's own wire contracts).
type fakeChildren struct {
	mu    sync.Mutex
	byKey map[string]*job.Job
	order []string
	// failKeys terminally fails the child at creation (broker-side
	// failure). waitErrKeys simulates a PROCESS DEATH mid-stage: the
	// child is alive but the first wait for it is interrupted (the
	// worker died before observing its terminal state).
	failKeys        map[string]bool
	waitErrKeys     map[string]bool
	waitErrIDs      map[string]bool
	missingHandlers map[string]bool
}

func newFakeChildren() *fakeChildren {
	return &fakeChildren{
		byKey: map[string]*job.Job{}, failKeys: map[string]bool{},
		waitErrKeys: map[string]bool{}, waitErrIDs: map[string]bool{}, missingHandlers: map[string]bool{},
	}
}

// failNextWaitOnce marks the child created under idemKey so its FIRST
// WaitTerminal fails once (crash simulation). It may be called before
// the child exists.
func (f *fakeChildren) failNextWaitOnce(idemKey string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if j, ok := f.byKey[idemKey]; ok {
		f.waitErrIDs[j.ID] = true
		return
	}
	f.waitErrKeys[idemKey] = true
}

// enqueueCount is the number of DISTINCT children ever created.
func (f *fakeChildren) enqueueCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.order)
}

func (f *fakeChildren) HasHandler(jobType string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return !f.missingHandlers[jobType]
}

func (f *fakeChildren) EnqueueChild(_ context.Context, req ChildJobRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.byKey[req.IdempotencyKey]; ok {
		return existing.ID, nil
	}
	id := fmt.Sprintf("child_%03d_%s", len(f.order)+1, digest.SHA256String(req.IdempotencyKey)[:8])
	result := cannedChildResult(req.JobType, id, req.IdempotencyKey, req.Payload)
	status := job.StatusSucceeded
	errText := ""
	if f.failKeys[req.IdempotencyKey] {
		status = job.StatusFailed
		errText = "injected child failure"
	}
	f.byKey[req.IdempotencyKey] = &job.Job{
		ID: id, Type: req.JobType, Project: req.Project, VideoName: req.VideoName,
		CorrelationID: req.CorrelationID, IdempotencyKey: req.IdempotencyKey,
		Status: status, Error: errText, Result: result,
	}
	f.order = append(f.order, req.IdempotencyKey)
	// voiceover.generate is an EXTERNAL-SAFE parent: its real contract
	// fans out one generate_item child per item and reports their ids in
	// the fan-out result map. The fake reproduces that fan-out so the
	// workflow's item waits run against the real parent→item shape.
	if req.JobType == job.TypeVoiceoverGenerate && status == job.StatusSucceeded {
		itemIDs, languages := f.spawnVoiceoverItemsLocked(req)
		f.byKey[req.IdempotencyKey].Result = cannedVoiceoverFanoutResult(id, req.CorrelationID, itemIDs, languages)
	}
	if f.waitErrKeys[req.IdempotencyKey] {
		delete(f.waitErrKeys, req.IdempotencyKey)
		f.waitErrIDs[id] = true
	}
	return id, nil
}

func (f *fakeChildren) WaitTerminal(_ context.Context, childJobID string) (*job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.waitErrIDs[childJobID] {
		delete(f.waitErrIDs, childJobID)
		return nil, fmt.Errorf("fakeChildren: wait interrupted (process death) for %s", childJobID)
	}
	for _, j := range f.byKey {
		if j.ID == childJobID {
			copied := *j
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("fakeChildren: unknown child %s", childJobID)
}

// ── REAL child wire shapes ───────────────────────────────────────────
//
// Every canned result below is the child family's OWN result wire
// contract — the exact JSON the registered handler writes into
// job.Result (script.generate's durable {run_id, parent_state, result,
// __artifact_manifest} envelope; youtube_clip.extract's
// ExtractResponse-shaped map; media.stock's StockJobResult.ToResultMap;
// voiceover.generate's fan-out map + generate_item item maps;
// clip.render's renderedResult map). children_contracts_test.go pins
// these against the handler-side types so a contract drift breaks the
// build instead of silently emptying a stage.

// cannedChildResult returns each family's REAL terminal result wire shape.
func cannedChildResult(jobType, id, key string, payload json.RawMessage) json.RawMessage {
	sha := digest.SHA256String(key)
	switch jobType {
	case job.TypeScriptGenerate:
		return cannedScriptResult(id, sha)
	case appjobs.TypeYouTubeClipExtract:
		return cannedYouTubeResult(payload, sha)
	case appjobs.TypeMediaStock:
		return cannedStockResult(id, sha)
	case job.TypeClipRender:
		return cannedRenderResult(id, payload, sha)
	default:
		return mustRaw(map[string]any{})
	}
}

// cannedScriptResult is the real durable single-item script.generate
// result map (scripts/jobs/generation_handler.go::Handle, durable
// branch), built from the handler-side capability result type.
func cannedScriptResult(id, sha string) json.RawMessage {
	lang := scriptgen.Language("en")
	res := scriptgen.GenerateResult{
		Output: scriptgen.GenerateOutput{Text: "Mike Tyson training\n\nchampionship rounds", WordCount: 6},
		Scenes: []scriptgen.Scene{
			{ID: "scene-001", Index: 0, Text: map[scriptgen.Language]string{lang: "Mike Tyson training"},
				Voiceover: map[scriptgen.Language]scriptgen.AudioReference{lang: {URL: "https://drive.test/vo-1", FilePath: "/fake/materialized/script-voiceover-1.aac", Duration: 3.5}}},
			{ID: "scene-002", Index: 1, Text: map[scriptgen.Language]string{lang: "championship rounds"},
				Voiceover: map[scriptgen.Language]scriptgen.AudioReference{lang: {URL: "https://drive.test/vo-2", FilePath: "/fake/materialized/script-voiceover-2.aac", Duration: 3.5}}},
		},
		AudioPlan: &audio.CompiledAudioPlan{Version: "compiled-audio-plan.v2"},
	}
	return mustRaw(scriptDurableWire{
		RunID:       "run_" + sha[:8],
		ParentState: "completed",
		Result:      &res,
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         id,
			Artifacts: []job.Artifact{{
				ID: "script:" + sha[:12], Kind: job.ArtifactKindScriptJSON,
				Path: "/fake/script.json", Filename: "script.json", MIMEType: "application/json",
				SHA256: sha,
			}},
		},
	})
}

// cannedYouTubeResult is the real youtube_clip.extract result map
// (youtube/jobs/job_handler.go::buildResultMap), projected through the
// handler-side ExtractResponse DTO.
func cannedYouTubeResult(payload json.RawMessage, sha string) json.RawMessage {
	var req youtubetypes.ExtractRequest
	_ = json.Unmarshal(payload, &req)
	return mustRaw(youtubetypes.ExtractResponse{
		OK:        true,
		SourceURL: req.URL,
		VideoID:   "yt_" + sha[:8],
		Stats:     &youtubetypes.ExtractStats{Requested: 1, Processed: 1},
		Items: []youtubetypes.ExtractItem{{
			ID: "yt_" + sha[:12], Name: "scene-001",
			Start: "00:00:00.000", End: "00:00:05.000",
			StartSeconds: 0, EndSeconds: 5, Duration: 5,
			LegacyFileMD5: sha, SizeBytes: 1024,
			LocalPath:   "/fake/materialized/yt_" + sha[:8] + ".mp4",
			DriveFileID: "drive_" + sha[:10], DriveLink: "https://drive.test/" + sha[:10],
			Status: "completed",
		}},
		DriveFolderID: "drive_folder_" + sha[:8],
	})
}

// cannedStockResult is the real media.stock result map
// (stockpipeline.StockJobResult.ToResultMap — the handler's own
// projection), so the workflow decodes exactly what production emits.
func cannedStockResult(id, sha string) json.RawMessage {
	res := stockpipeline.StockJobResult{
		FinalStatus: "completed",
		TotalClips:  1,
		TotalChunks: 1,
		Chunks: []stockpipeline.ChunkResult{{
			Index: 0, TimelineStart: 0, TimelineEnd: 5,
			LocalPath:   "/fake/materialized/stk_" + sha[:8] + ".mp4",
			DriveFileID: "drive_" + sha[:10], DriveLink: "https://drive.test/" + sha[:10],
			SHA256: sha, Title: "stock clip", Rendered: true, Uploaded: true,
		}},
		Manifest: &job.ArtifactManifest{
			SchemaVersion: job.SchemaVersionArtifactManifestV1,
			JobID:         id,
			Artifacts: []job.Artifact{{
				ID: "stock:" + sha[:12], Kind: "media",
				SHA256: sha, SizeBytes: 2048,
				ArtifactMetadata: map[string]any{"asset_id": "stock:" + sha[:12]},
			}},
		},
	}
	return mustRaw(res.ToResultMap())
}

// cannedRenderResult mirrors the clip.render result map
// (cliprender/worker_result.go::renderedResult) key for key.
func cannedRenderResult(id string, payload json.RawMessage, sha string) json.RawMessage {
	var req cliprender.RenderRequest
	_ = json.Unmarshal(payload, &req)
	return mustRaw(map[string]any{
		"job_id":          id,
		"source_asset_id": req.SourceAssetID,
		"phase":           "rendered",
		"transcript_mode": "reuse",
		"contract_id":     cliprender.OutputContractVeloxAssemblyReadyV1,
		"contract": map[string]any{
			"width": 1920, "height": 1080, "fps_num": 24, "fps_den": 1,
			"video_codec": "h264", "audio_codec": "aac", "pixel_format": "yuv420p",
		},
		"render": map[string]any{
			"output_path":         "/fake/materialized/" + sha[:8] + "_render.mp4",
			"size_bytes":          4096,
			"duration_sec":        5.0,
			"width":               1920,
			"height":              1080,
			"fps_num":             24,
			"fps_den":             1,
			"backend":             "chronon_vulkan",
			"audio_copy_eligible": true,
		},
		"asset": map[string]any{
			"asset_id":           "clip:" + sha[:12],
			"drive_file_id":      "drive_" + sha[:10],
			"drive_link":         "https://drive.test/" + sha[:10],
			"publication_status": "PUBLISHED",
			"size_bytes":         4096,
		},
	})
}

// cannedVoiceoverFanoutResult mirrors the voiceover.generate parent
// result map (voiceover/service/jobs/generate_handler.go::toFanoutResultMap).
func cannedVoiceoverFanoutResult(id, requestID string, itemIDs, languages []string) json.RawMessage {
	return mustRaw(map[string]any{
		"ok":                   true,
		"parent_job_id":        id,
		"request_id":           requestID,
		"total_outputs":        len(itemIDs),
		"enqueued_count":       len(itemIDs),
		"failed_enqueue_count": 0,
		"child_job_ids":        itemIDs,
		"per_language":         languages,
		"parent_state":         "AWAITING_CHILDREN",
	})
}

// cannedVoiceoverItemResult mirrors the voiceover.generate_item result
// map (voiceover/service/jobs/generate_item_handler.go::toItemResultMap).
func cannedVoiceoverItemResult(id, requestID, language, key string) json.RawMessage {
	sha := digest.SHA256String(key)
	return mustRaw(map[string]any{
		"job_id":        id,
		"request_id":    requestID,
		"language":      language,
		"status":        "completed",
		"ok":            true,
		"voice":         "default",
		"drive_link":    "https://drive.test/" + sha[:10],
		"drive_file_id": "drive_" + sha[:10],
		"local_path":    "/fake/materialized/voiceover-" + sha[:6] + ".aac",
		"error":         "",
		"error_code":    "",
		"timing":        map[string]any{"duration_us": 3_500_000},
	})
}

// spawnVoiceoverItemsLocked registers one generate_item child per
// command item and returns their ids + languages (the fan-out the real
// voiceover.generate parent performs).
func (f *fakeChildren) spawnVoiceoverItemsLocked(req ChildJobRequest) (itemIDs, languages []string) {
	var cmd struct {
		Items []struct {
			Language string `json:"language"`
		} `json:"items"`
	}
	_ = json.Unmarshal(req.Payload, &cmd)
	for i, item := range cmd.Items {
		itemKey := fmt.Sprintf("%s:item:%03d", req.IdempotencyKey, i+1)
		lang := item.Language
		if lang == "" {
			lang = "en"
		}
		if existing, ok := f.byKey[itemKey]; ok {
			itemIDs = append(itemIDs, existing.ID)
			languages = append(languages, lang)
			continue
		}
		itemID := fmt.Sprintf("child_%03d_%s", len(f.order)+1, digest.SHA256String(itemKey)[:8])
		f.byKey[itemKey] = &job.Job{
			ID: itemID, Type: job.TypeVoiceoverGenerateItem,
			Project: req.Project, VideoName: req.VideoName,
			CorrelationID:  ChildCorrelationID(req.CorrelationID, job.TypeVoiceoverGenerateItem, itemKey),
			IdempotencyKey: itemKey,
			Status:         job.StatusSucceeded,
			Result:         cannedVoiceoverItemResult(itemID, req.CorrelationID, lang, itemKey),
		}
		f.order = append(f.order, itemKey)
		itemIDs = append(itemIDs, itemID)
		languages = append(languages, lang)
	}
	return itemIDs, languages
}

func mustRaw(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// ── The remaining ports ───────────────────────────────────────────────

type fakeSearch struct{ candidates []MediaCandidate }

func (f fakeSearch) Search(_ context.Context, req MediaSearchRequest) ([]MediaCandidate, error) {
	if len(f.candidates) > 0 {
		return f.candidates, nil
	}
	out := make([]MediaCandidate, 0, req.Limit)
	for i := 0; i < req.Limit; i++ {
		source := "stock"
		if i%2 == 0 {
			source = "youtube"
		}
		out = append(out, MediaCandidate{
			AssetID: fmt.Sprintf("cand-%02d", i), Source: source,
			SourceURL: fmt.Sprintf("https://example.test/%s/%d", source, i),
			Title:     fmt.Sprintf("candidate %d", i), MediaType: "video",
			DurationMS: 8000, Score: float64(req.Limit - i),
		})
	}
	return out, nil
}

type fakeAudio struct{}

func (fakeAudio) Master(_ context.Context, req MasterRequest) (MasteredAudio, error) {
	_ = os.WriteFile(req.OutputPath, []byte("fake canonical final audio"), 0o644)
	return MasteredAudio{
		Asset: audio.FinalAudioAsset{
			AssetID: "final_audio:fake", AudioContractVersion: audio.AudioContractVersion,
			FinalAudioSHA256: digest.SHA256String(req.OutputPath), DurationMS: 58000,
			SampleRate: 48000, Channels: 2, Codec: "aac", Profile: "LC",
		},
		Path: req.OutputPath,
	}, nil
}

func (fakeAudio) Mux(_ context.Context, req MuxRequest) (MuxedVideo, error) {
	_ = os.WriteFile(req.OutputPath, []byte("fake final video with audio"), 0o644)
	return MuxedVideo{Path: req.OutputPath}, nil
}

type fakeProbe struct{ facts ProbeFacts }

func (f fakeProbe) Probe(_ context.Context, path string) (ProbeFacts, error) {
	facts := f.facts
	if facts == (ProbeFacts{}) {
		facts = ProbeFacts{
			Path: path, SizeBytes: 12_345_678, DurationMS: 60_123,
			Width: 1920, Height: 1080, FPS: 30,
			VideoCodec: "h264", AudioCodec: "aac",
			SampleRate: 48000, Channels: 2,
			VideoStreamCount: 1, AudioStreamCount: 1,
		}
	}
	facts.Path = path
	return facts, nil
}

type fakeAssembler struct {
	calls int
	dir   string
}

func (f *fakeAssembler) Assemble(_ context.Context, req AssembleRequest) (AssembleResult, error) {
	f.calls++
	path := req.OutputPath
	if path == "" {
		path = filepath.Join(f.dir, "assembled.mp4")
	}
	_ = os.WriteFile(path, []byte("fake assembled video"), 0o644)
	var totalMS int64
	for _, seg := range req.Segments {
		totalMS += seg.DurationMS
	}
	return AssembleResult{
		ArtifactID: "assembled:" + digest.SHA256String(req.AssemblyID)[:12],
		Path:       path,
		DurationMS: totalMS,
	}, nil
}

type fakePublisher struct{ calls int }

func (f *fakePublisher) Publish(_ context.Context, req PublishRequest) (PublishedArtifact, error) {
	f.calls++
	return PublishedArtifact{
		AssetID: req.AssetID, DriveRef: DriveRef{DriveFileID: "drive_file_final"},
		MediaURL: "https://drive.test/final_video", DownloadURL: "https://drive.test/final_video/download",
		SHA256: req.SHA256, SizeBytes: req.SizeBytes,
	}, nil
}

var (
	_ ChildJobs         = (*fakeChildren)(nil)
	_ MediaSearch       = fakeSearch{}
	_ AudioMaster       = fakeAudio{}
	_ MediaProber       = fakeProbe{}
	_ Assembler         = (*fakeAssembler)(nil)
	_ ArtifactPublisher = (*fakePublisher)(nil)
	_ steps.Store       = (*fakeStore)(nil)
)

// newTestDeps assembles the hermetic dependency bundle. probe may be
// zero-valued (the fake then answers with a valid audible MP4).
func newTestDeps(t interface{ TempDir() string }, children *fakeChildren, probe fakeProbe) (Deps, *fakeAssembler, *fakePublisher) {
	dir := t.TempDir()
	assembler := &fakeAssembler{dir: dir}
	publisher := &fakePublisher{}
	deps := Deps{
		Steps:     newFakeStore(),
		Children:  children,
		Search:    fakeSearch{},
		Audio:     fakeAudio{},
		Probe:     probe,
		Assembler: assembler,
		Publish:   publisher,
		Workspace: dir,
	}
	return deps, assembler, publisher
}
