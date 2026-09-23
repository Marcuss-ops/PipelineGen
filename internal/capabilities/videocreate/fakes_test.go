package videocreate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	audio "github.com/Marcuss-ops/PipelineGen/internal/capabilities/audio"
	steps "github.com/Marcuss-ops/PipelineGen/internal/capabilities/execution/steps"
	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs"
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

// cannedChildResult returns each family's typed terminal result.
func cannedChildResult(jobType, id, key string, payload json.RawMessage) json.RawMessage {
	sha := digest.SHA256String(key)
	switch jobType {
	case job.TypeScriptGenerate:
		return mustRaw(ScriptChildResult{
			ScriptAssetID: "script:" + sha[:12],
			Scenes:        []string{"scene-001", "scene-002"},
			TextSegments:  []string{"Mike Tyson training", "championship rounds"},
			AudioPlan:     json.RawMessage(`{"audio_plan_version":"compiled-audio-plan.v2","timeline_version":"canonical-timeline.v2"}`),
		})
	case appjobs.TypeYouTubeClipExtract, appjobs.TypeMediaStock:
		var req MediaAcquireChildRequest
		_ = json.Unmarshal(payload, &req)
		source := "stock"
		if jobType == appjobs.TypeYouTubeClipExtract {
			source = "youtube"
		}
		return mustRaw(AcquireChildResult{
			AssetID: source + ":" + sha[:12], ContentSHA: sha,
			DurationMS: 5000,
			MediaType:  "video", Source: source, SourceURL: req.SourceURL,
			DriveRef: DriveRef{DriveFileID: "drive_" + sha[:10]},
			LocalRef: LocalRef{LocalPath: "/fake/materialized/" + key + ".mp4"},
		})
	case job.TypeVoiceoverGenerate:
		return mustRaw(VoiceoverChildResult{
			AssetID: "voiceover:" + sha[:12], ContentSHA: sha, DurationMS: 58000,
			SampleRate: 48000, Channels: 2, Codec: "aac",
			LocalRef:  LocalRef{LocalPath: "/fake/materialized/voiceover.aac"},
			AudioPlan: json.RawMessage(`{"audio_plan_version":"compiled-audio-plan.v2","timeline_version":"canonical-timeline.v2"}`),
		})
	case job.TypeClipRender:
		return mustRaw(RenderChildResult{
			AssetID: "clip:" + sha[:12], ContentSHA: sha, DurationMS: 60000,
			LocalRef:        LocalRef{LocalPath: "/fake/materialized/" + key + "_render.mp4"},
			CopyCertified:   true,
			ContractID:      "assembly-contract.v2",
			StreamSignature: "h264:1920x1080:30:1:aac",
		})
	case appjobs.TypeAssemblyPrepare:
		return mustRaw(map[string]any{"preparation_id": id})
	case appjobs.TypeAssemblyFinalize:
		return mustRaw(map[string]any{"artifact_id": "assembled:" + sha[:12], "artifact_path": "/fake/materialized/assembled.mp4"})
	default:
		return mustRaw(map[string]any{})
	}
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
	path := filepath.Join(f.dir, "assembled.mp4")
	_ = os.WriteFile(path, []byte("fake assembled video"), 0o644)
	return AssembleResult{
		ArtifactID:  "assembled:" + digest.SHA256String(req.AssemblyID)[:12],
		Path:        path,
		ChildJobIDs: []string{"child_prepare_fake", "child_finalize_fake"},
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
