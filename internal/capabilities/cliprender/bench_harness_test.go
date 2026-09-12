package cliprender

// bench_harness_test.go owns the canonical clip.render performance benchmark
// harness. It exists because the acceptance criterion for the async
// submit/settle split (TICKET-CLIP-RENDER-ASYNC-COMPLETION §5) is a
// before/after throughput report proving the Master slot is NOT occupied while
// Chronon renders — and because "is the pipeline fast" is a question about
// clips/minute at steady state, not about one clip's wall time.
//
// The harness is deterministic and CI-safe:
//
//   - every external boundary (RenderingGen/Chronon lane, artifact download,
//     Drive publication, asset materialization, transcript ASR) is a
//     latency-configurable fake whose delay IS a parameter, so a scenario
//     states "render = 20 ms, download = 5 ms" instead of depending on a GPU;
//   - the JOB PIPELINE is real: the driver runs the actual Worker.Handle
//     (both the submit and the settle phase), the actual continuation
//     contract, the actual ParentAggregator and the actual kernel RunReport
//     instrumentation;
//   - it models the two things that actually bound throughput: N Master
//     worker slots and L RenderingGen GPU lanes.
//
// Real-stack runs reuse the same report schema; the live-only scenario in
// bench_seek_test.go documents the procedure (VELOX_BENCH_REAL_STACK=1).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"go.uber.org/zap"
)

// ── Report schema ────────────────────────────────────────────────────────

// benchPhases is the per-clip phase breakdown of the canonical benchmark.
// Every value is milliseconds and comes from a real chronometer: the kernel
// RunReport stages for the worker-owned phases, the RenderingGen lane
// simulator for the remote half. Phase names follow the performance spec so
// two runs are always comparable.
type benchPhases struct {
	PrepareMS   int64 `json:"prepare_ms"`
	SubtitlesMS int64 `json:"subtitles_ms"`
	SubmitMS    int64 `json:"submit_ms"`
	SettleMS    int64 `json:"settle_ms"`
	RenderMS    int64 `json:"render_ms"`
	DownloadMS  int64 `json:"artifact_download_ms"`
	ProbeMS     int64 `json:"probe_ms"`
	PublishMS   int64 `json:"publish_ms"`
	QueueWaitMS int64 `json:"queue_wait_ms"`
	ParentMS    int64 `json:"parent_finalize_ms"`
	OccupiedMS  int64 `json:"worker_slot_occupied_ms"`
}

// benchClip is one clip's row in the report.
type benchClip struct {
	Index  int         `json:"index"`
	RunID  string      `json:"run_id"`
	Phases benchPhases `json:"phases"`
	E2EMS  int64       `json:"e2e_ms"`
	Failed bool        `json:"failed"`
	Error  string      `json:"error,omitempty"`
}

// benchReport is the canonical benchmark artifact. It is the same shape for
// every scenario so a regression tool can diff two runs field by field.
type benchReport struct {
	Scenario    string `json:"scenario"`
	Mode        string `json:"mode"` // async | blocking
	Workers     int    `json:"workers"`
	WaiterPool  int    `json:"waiter_pool"`
	GPULanes    int    `json:"gpu_lanes"`
	AggregateMS int64  `json:"aggregate_interval_ms"`
	EventDriven bool   `json:"event_driven"`
	Clips       int    `json:"clips"`

	WallMS      int64   `json:"wall_ms"`
	ClipsPerMin float64 `json:"clips_per_min"`
	P50MS       int64   `json:"p50_ms"`
	P95MS       int64   `json:"p95_ms"`
	MeanMS      int64   `json:"mean_ms"`

	SubmitOccupancyP50MS int64 `json:"submit_occupancy_p50_ms"`
	SubmitOccupancyP95MS int64 `json:"submit_occupancy_p95_ms"`
	SettleOccupancyP50MS int64 `json:"settle_occupancy_p50_ms"`
	SettleOccupancyP95MS int64 `json:"settle_occupancy_p95_ms"`
	RemoteRenderP50MS    int64 `json:"remote_render_p50_ms"`
	RemoteQueueP95MS     int64 `json:"remote_queue_p95_ms"`
	ParentFinalizeP50MS  int64 `json:"parent_finalize_p50_ms"`
	ParentFinalizeP95MS  int64 `json:"parent_finalize_p95_ms"`

	GPULaneBusyMS    int64   `json:"gpu_lane_busy_ms"`
	GPULaneUtilPct   float64 `json:"gpu_lane_utilization_pct"`
	GPULaneIdlePct   float64 `json:"gpu_lane_idle_pct"`
	GPUMaxConcurrent int     `json:"gpu_lane_max_concurrent"`
	AggregatedFPS    float64 `json:"aggregated_fps"`

	DiskReadBytes  int64 `json:"disk_read_bytes"`
	DiskWriteBytes int64 `json:"disk_write_bytes"`
	NetworkTXBytes int64 `json:"network_tx_bytes"`
	NetworkRXBytes int64 `json:"network_rx_bytes"`

	SourceFullHashes int64 `json:"source_full_hashes"`
	SourceDownloads  int64 `json:"source_downloads"`

	Submits  int `json:"submits"`
	Settles  int `json:"settles"`
	Failures int `json:"failures"`

	StartedAt  string      `json:"started_at"`
	FinishedAt string      `json:"finished_at"`
	ClipRuns   []benchClip `json:"clip_runs,omitempty"`
}

// ── Statistics helpers ───────────────────────────────────────────────────

// benchPercentile returns the p-th percentile (0..100) of values using the
// nearest-rank method. Empty input returns 0.
func benchPercentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if p <= 0 {
		return sorted[0]
	}
	if p >= 100 {
		return sorted[len(sorted)-1]
	}
	rank := int(float64(len(sorted)) * p / 100)
	if rank >= len(sorted) {
		rank = len(sorted) - 1
	}
	return sorted[rank]
}

// benchMean returns the arithmetic mean, or 0 for empty input.
func benchMean(values []int64) int64 {
	if len(values) == 0 {
		return 0
	}
	var sum int64
	for _, v := range values {
		sum += v
	}
	return sum / int64(len(values))
}

// benchClipsPerMin converts a batch of n clips finished in d into a rate.
func benchClipsPerMin(n int, d time.Duration) float64 {
	if n <= 0 || d <= 0 {
		return 0
	}
	return float64(n) / d.Seconds() * 60
}

// benchSpeedup is the throughput multiple of a run over its baseline.
func benchSpeedup(baselineClipsPerMin, actualClipsPerMin float64) float64 {
	if baselineClipsPerMin <= 0 {
		return 0
	}
	return actualClipsPerMin / baselineClipsPerMin
}

// markdown renders the report as the human-readable table operators eyeball
// after a change; the JSON artifact stays the machine-readable form.
func (r benchReport) markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s | mode=%s workers=%d waiters=%d lanes=%d clips=%d event_driven=%v |\n",
		r.Scenario, r.Mode, r.Workers, r.WaiterPool, r.GPULanes, r.Clips, r.EventDriven)
	b.WriteString("| wall_ms | clips/min | p50 | p95 | submit_p50 | settle_p50 | render_p50 | queue_p95 | parent_p50 | gpu_util%% | gpu_idle%% |\n")
	fmt.Fprintf(&b, "| %d | %.2f | %d | %d | %d | %d | %d | %d | %d | %.1f | %.1f |\n",
		r.WallMS, r.ClipsPerMin, r.P50MS, r.P95MS,
		r.SubmitOccupancyP50MS, r.SettleOccupancyP50MS, r.RemoteRenderP50MS, r.RemoteQueueP95MS,
		r.ParentFinalizeP50MS, r.GPULaneUtilPct, r.GPULaneIdlePct)
	fmt.Fprintf(&b, "| hashes=%d downloads=%d disk_read=%d disk_write=%d net_tx=%d net_rx=%d fps=%.1f failures=%d |\n",
		r.SourceFullHashes, r.SourceDownloads, r.DiskReadBytes, r.DiskWriteBytes,
		r.NetworkTXBytes, r.NetworkRXBytes, r.AggregatedFPS, r.Failures)
	return b.String()
}

// writeBenchReport persists the report. Reports land in the canonical results
// tree only when VELOX_BENCH_WRITE_REPORT=1 (the historical audit found the
// results corpus being destroyed by hand); otherwise they stay in the test's
// temp dir so a normal run never dirties the working tree.
func writeBenchReport(t *testing.T, r benchReport) string {
	t.Helper()
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatalf("marshal bench report: %v", err)
	}
	dir := t.TempDir()
	if os.Getenv("VELOX_BENCH_WRITE_REPORT") == "1" {
		dir = filepath.Join("..", "..", "..", "tests", "operational", "results", "cliprender-bench")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create bench report dir: %v", err)
		}
	}
	path := filepath.Join(dir, r.Scenario+".json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write bench report: %v", err)
	}
	t.Logf("\n%s\nreport: %s", r.markdown(), path)
	return path
}

// ── RenderingGen / Chronon lane simulator ────────────────────────────────

// benchRemote models the RenderingGen/Chronon boundary with the one property
// that actually bounds clip throughput: a fixed number of GPU lanes. Submit
// is non-blocking (job accepted); Settle waits for a lane, "renders" for
// renderMS and "downloads the certified artifact" for downloadMS. Both halves
// are timed, so queue wait is visible and separable from render service.
type benchRemote struct {
	lanes      int
	renderMS   time.Duration
	downloadMS time.Duration
	lane       chan struct{}

	mu            sync.Mutex
	accepted      map[string]time.Time
	completed     map[string]time.Time
	queueWait     map[string]time.Duration
	serviceWall   map[string]time.Duration
	downloadWall  map[string]time.Duration
	submits       int
	settles       int
	inLane        int
	maxConcurrent int
	lanesBusy     time.Duration
	spanStart     time.Time
	spanEnd       time.Time
}

func newBenchRemote(lanes int, renderMS, downloadMS time.Duration) *benchRemote {
	if lanes < 1 {
		lanes = 1
	}
	return &benchRemote{
		lanes: lanes, renderMS: renderMS, downloadMS: downloadMS,
		lane:      make(chan struct{}, lanes),
		accepted:  map[string]time.Time{},
		completed: map[string]time.Time{}, queueWait: map[string]time.Duration{},
		serviceWall:  map[string]time.Duration{},
		downloadWall: map[string]time.Duration{},
	}
}

func (r *benchRemote) Submit(_ context.Context, plan ClipRenderPlanV1) error {
	now := time.Now()
	r.mu.Lock()
	r.submits++
	r.accepted[plan.RunID] = now
	if r.spanStart.IsZero() {
		r.spanStart = now
	}
	r.mu.Unlock()
	return nil
}

func (r *benchRemote) Settle(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error) {
	waitStart := time.Now()
	select {
	case r.lane <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.lane }()

	queueWait := time.Since(waitStart)
	serviceStart := time.Now()
	r.mu.Lock()
	r.inLane++
	if r.inLane > r.maxConcurrent {
		r.maxConcurrent = r.inLane
	}
	r.mu.Unlock()

	if err := benchSleep(ctx, r.renderMS); err != nil {
		return nil, err
	}
	downloadStart := time.Now()
	if err := benchSleep(ctx, r.downloadMS); err != nil {
		return nil, err
	}
	downloadWall := time.Since(downloadStart)
	serviceWall := time.Since(serviceStart)
	finished := time.Now()

	r.mu.Lock()
	r.inLane--
	r.lanesBusy += serviceWall
	r.settles++
	r.queueWait[plan.RunID] = queueWait
	r.serviceWall[plan.RunID] = serviceWall
	r.downloadWall[plan.RunID] = downloadWall
	r.completed[plan.RunID] = finished
	r.spanEnd = finished
	r.mu.Unlock()

	return benchOutcome(plan), nil
}

// Render is the blocking form (Submit + Settle). It exists so the same
// simulator can drive the pre-split pipeline, which is what makes the
// before/after comparison honest.
func (r *benchRemote) Render(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error) {
	if err := r.Submit(ctx, plan); err != nil {
		return nil, err
	}
	return r.Settle(ctx, plan)
}

// benchRemoteStats is a consistent snapshot of the lane telemetry.
type benchRemoteStats struct {
	Lanes         int
	Submits       int
	Settles       int
	MaxConcurrent int
	LanesBusy     time.Duration
	Span          time.Duration
}

func (r *benchRemote) stats() benchRemoteStats {
	r.mu.Lock()
	defer r.mu.Unlock()
	var span time.Duration
	if !r.spanStart.IsZero() && r.spanEnd.After(r.spanStart) {
		span = r.spanEnd.Sub(r.spanStart)
	}
	return benchRemoteStats{
		Lanes: r.lanes, Submits: r.submits, Settles: r.settles,
		MaxConcurrent: r.maxConcurrent, LanesBusy: r.lanesBusy, Span: span,
	}
}

// utilization is lane utilization: busy lane-time / (span × lanes).
func (s benchRemoteStats) utilization() float64 {
	if s.Span <= 0 {
		return 0
	}
	total := float64(s.Span) * float64(benchMax(s.Lanes, 1))
	if total <= 0 {
		return 0
	}
	f := float64(s.LanesBusy) / total
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

func (r *benchRemote) queueWaitP95() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	vals := make([]int64, 0, len(r.queueWait))
	for _, v := range r.queueWait {
		vals = append(vals, v.Milliseconds())
	}
	return benchPercentile(vals, 95)
}

// downloadMSFor reports the artifact-download wall for one render, so the
// report can attribute the certified-artifact transfer separately from the
// render service time.
func (r *benchRemote) downloadMSFor(runID string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.downloadWall[runID].Milliseconds()
}

func (r *benchRemote) serviceP50() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	vals := make([]int64, 0, len(r.serviceWall))
	for _, v := range r.serviceWall {
		vals = append(vals, v.Milliseconds())
	}
	return benchPercentile(vals, 50)
}

// benchBlockingRenderer exposes ONLY RenderExecutor, so the worker takes its
// blocking path — the model of the pre-split pipeline.
type benchBlockingRenderer struct{ remote *benchRemote }

func (b *benchBlockingRenderer) Render(ctx context.Context, plan ClipRenderPlanV1) (*RenderOutcome, error) {
	return b.remote.Render(ctx, plan)
}

// benchOutcome is a valid certified Chronon outcome for a sealed plan. The
// digest is a fixed 64-hex string because the fake boundary does not re-hash;
// the pipeline only requires a non-empty certified digest.
func benchOutcome(plan ClipRenderPlanV1) *RenderOutcome {
	fpsNum := plan.Output.FPSNum
	if fpsNum <= 0 {
		fpsNum = 24
	}
	fpsDen := plan.Output.FPSDen
	if fpsDen <= 0 {
		fpsDen = 1
	}
	return &RenderOutcome{
		OutputPath:  plan.OutputPath,
		SizeBytes:   4096,
		SHA256:      strings.Repeat("a", 64),
		DurationSec: 3,
		Width:       uint32(plan.Output.Width),
		Height:      uint32(plan.Output.Height),
		FPSNum:      uint32(fpsNum),
		FPSDen:      uint32(fpsDen),
		Backend:     BackendChrononVulkan,
		FFmpegMS:    1,
	}
}

// benchSleep is an interruption-aware delay.
func benchSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ── Bench ports ──────────────────────────────────────────────────────────

// benchContinuationStore is the in-memory CAS-backed resume store the settle
// phase resumes from, keyed by the document's own content address.
type benchContinuationStore struct {
	mu   sync.Mutex
	docs map[string]ResumeDocument
}

func newBenchContinuationStore() *benchContinuationStore {
	return &benchContinuationStore{docs: map[string]ResumeDocument{}}
}

func (s *benchContinuationStore) PutResumeDocument(_ context.Context, doc ResumeDocument) (ContinuationRef, error) {
	if err := doc.Validate(); err != nil {
		return ContinuationRef{}, err
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ContinuationRef{}, fmt.Errorf("bench continuation store: marshal: %w", err)
	}
	ref := ContinuationRef{SHA256: digest.SHA256Bytes(raw), SizeBytes: int64(len(raw))}
	s.mu.Lock()
	s.docs[ref.SHA256] = doc
	s.mu.Unlock()
	return ref, nil
}

func (s *benchContinuationStore) GetResumeDocument(_ context.Context, ref ContinuationRef) (ResumeDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	doc, ok := s.docs[ref.SHA256]
	if !ok {
		return ResumeDocument{}, fmt.Errorf("bench continuation store: unknown resume document %s", ref.SHA256)
	}
	return doc, nil
}

// benchPublisher is the Drive publication boundary with a configurable wall.
type benchPublisher struct {
	delay time.Duration

	mu        sync.Mutex
	calls     int
	wall      []time.Duration
	bytes     int64
	networkTX int64
}

func (p *benchPublisher) Publish(_ context.Context, in RenderPublishInput) (*RenderPublishResult, error) {
	start := time.Now()
	elapsed := time.Since(start)
	if p.delay > 0 {
		time.Sleep(p.delay)
		elapsed = time.Since(start)
	}
	size := int64(0)
	if in.Outcome != nil {
		size = in.Outcome.SizeBytes
	}
	p.mu.Lock()
	p.calls++
	p.wall = append(p.wall, elapsed)
	p.bytes += size
	p.networkTX += size
	p.mu.Unlock()
	ms := elapsed.Milliseconds()
	return &RenderPublishResult{
		AssetID:     "final-" + in.RunID,
		DriveFileID: "drive-" + in.RunID,
		DriveLink:   "https://drive.example/" + in.RunID,
		SizeBytes:   size,
		Publish:     &PublicationMetrics{TotalMS: ms, VideoUploadMS: ms},
	}, nil
}

func (p *benchPublisher) snapshot() (calls int, p50MS int64, bytes int64, networkTX int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	vals := make([]int64, 0, len(p.wall))
	for _, w := range p.wall {
		vals = append(vals, w.Milliseconds())
	}
	return p.calls, benchPercentile(vals, 50), p.bytes, p.networkTX
}

// benchProber is the post-render byte certification boundary with a
// configurable probe wall. The probe values are exactly what the default
// VELOX_ASSEMBLY_READY_V1 contract requires, so wiring it exercises the real
// ValidateContract instead of skipping certification.
type benchProber struct {
	delay time.Duration

	mu   sync.Mutex
	wall []time.Duration
}

func (p *benchProber) ProbeOutput(_ context.Context, _ string) (*OutputProbe, error) {
	start := time.Now()
	if err := benchSleep(context.Background(), p.delay); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.wall = append(p.wall, time.Since(start))
	p.mu.Unlock()
	return &OutputProbe{
		Container: "mp4", HasVideo: true, HasAudio: true,
		VideoCodec: "h264", VideoProfile: "high", PixelFormat: "yuv420p",
		Width: 1920, Height: 1080, FPS: 24.0, FPSNum: 24, FPSDen: 1,
		AudioCodec: "aac", AudioProfile: "LC", SampleRate: 48000, Channels: 2,
		ChannelLayout: "stereo", AudioBitrate: "128k",
		VideoStreams: 1, AudioStreams: 1, StartPTS: 0,
	}, nil
}

// callCount reports how many times the materializer was invoked (one full
// byte read/download each).
func (f *fakeMaterializer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// benchJobsService is the durable job surface the real ParentAggregator reads.
// It records when each parent was finalised, which is the latency the caller
// actually waits on.
type benchJobsService struct {
	mu          sync.Mutex
	parents     map[string]*job.Job
	children    map[string]*job.Job
	finalizedAt map[string]time.Time
	finalized   map[string]job.Status
	calls       int
}

func newBenchJobsService() *benchJobsService {
	return &benchJobsService{
		parents:     map[string]*job.Job{},
		children:    map[string]*job.Job{},
		finalizedAt: map[string]time.Time{},
		finalized:   map[string]job.Status{},
	}
}

func (s *benchJobsService) Register(parent *job.Job, childID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.parents[parent.ID] = parent
	s.children[childID] = &job.Job{ID: childID, Type: TypeClipRender, Status: job.StatusRunning}
}

func (s *benchJobsService) CompleteChild(childID string, status job.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[childID]; ok {
		c.Status = status
	}
}

func (s *benchJobsService) Get(_ context.Context, id string) (*job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.children[id]; ok {
		clone := *c
		return &clone, nil
	}
	if p, ok := s.parents[id]; ok {
		clone := *p
		return &clone, nil
	}
	return nil, fmt.Errorf("bench jobs service: unknown job %s", id)
}

func (s *benchJobsService) ListAwaitingAggregation(_ context.Context, _ string, _ int) ([]job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]job.Job, 0, len(s.parents))
	for id, p := range s.parents {
		if _, done := s.finalizedAt[id]; done {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (s *benchJobsService) FinalizeAggregateParent(_ context.Context, id string, status job.Status, _ map[string]any, _ string, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.finalizedAt[id] = time.Now()
	s.finalized[id] = status
	return nil
}

// finalizedLatency returns remote-completed → parent-finalized per run id.
func (s *benchJobsService) finalizedLatency(remote *benchRemote, parentsByRunID map[string]string) map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	remote.mu.Lock()
	defer remote.mu.Unlock()
	out := map[string]int64{}
	for runID, parentID := range parentsByRunID {
		done, ok := s.finalizedAt[parentID]
		if !ok {
			continue
		}
		remoteDone, ok := remote.completed[runID]
		if !ok || done.Before(remoteDone) {
			continue
		}
		out[runID] = done.Sub(remoteDone).Milliseconds()
	}
	return out
}

// ── Job pipeline driver ──────────────────────────────────────────────────

type benchJobKind int

const (
	benchJobSubmit benchJobKind = iota
	benchJobSettle
)

type benchJob struct {
	index    int
	kind     benchJobKind
	jobID    string
	childID  string
	runID    string
	payload  json.RawMessage
	enqueued time.Time
}

// benchConfig is one pipeline run.
type benchConfig struct {
	Scenario string
	Clips    int

	// Master worker slots and, when greater than zero, the size of the
	// dedicated settle pool (the P0.5 guardrail). Zero means submit and
	// settle share the same pool — the model of "no dedicated budget".
	Workers    int
	WaiterPool int

	// RenderingGen concurrency. This is the GPU lane count; it is the hard
	// ceiling on concurrent renders regardless of how many workers exist.
	GPULanes   int
	RenderMS   time.Duration
	DownloadMS time.Duration
	ProbeMS    time.Duration
	PublishMS  time.Duration
	PrepareMS  time.Duration
	Async      bool

	// AggregateInterval > 0 starts the real ParentAggregator ticker.
	// EventDriven finalises a parent synchronously as soon as its settle child
	// completes (the "finalizzazione immediata dal settle" case).
	AggregateInterval time.Duration
	EventDriven       bool
}

// benchPipeline runs a config against the REAL worker and returns the report.
func benchPipeline(t *testing.T, cfg benchConfig) benchReport {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	workspace := t.TempDir()
	remote := newBenchRemote(cfg.GPULanes, cfg.RenderMS, cfg.DownloadMS)
	jobsSvc := newBenchJobsService()
	store := newBenchContinuationStore()
	publisher := &benchPublisher{delay: cfg.PublishMS}

	// A distinct source per clip so the harness measures a true batch, not a
	// single memoised asset (the shared-source scenarios are separate).
	resolver := newFakeAssetResolver(map[string]AssetRef{})
	mat := &fakeMaterializer{delay: cfg.PrepareMS}
	tr := &fakeTranscriptResolver{
		existing: &TranscriptResult{
			Language: "en",
			Text:     "existing",
			Cues:     []Cue{{StartMs: 0, EndMs: 1000, Text: "existing"}},
			Reused:   true,
		},
		existingOK: true,
	}
	preparer := newTestPreparer(resolver, mat, tr)
	worker, err := NewWorker(preparer, workspace, zap.NewNop())
	if err != nil {
		t.Fatalf("bench: NewWorker: %v", err)
	}
	worker.WithRenderPublisher(publisher)
	// The prober is always wired: probe_ms must be a measurement, not a
	// NOT_INSTRUMENTED gap, and the contract validation it triggers is part of
	// the pipeline under test.
	worker.WithOutputProber(&benchProber{delay: cfg.ProbeMS})
	if cfg.Async {
		worker.WithRenderExecutor(remote).WithContinuationStore(store)
	} else {
		worker.WithRenderExecutor(&benchBlockingRenderer{remote: remote})
	}

	q := newBenchQueue(cfg)
	if cfg.Async {
		worker.WithContinuationEnqueuer(benchEnqueuer{fn: func(cont Continuation) (string, error) {
			payload, err := encodeContinuationPayload(cont)
			if err != nil {
				return "", err
			}
			return q.pushSettle(cont.Submission.RenderJobID, payload), nil
		}})
	}

	var resultsMu sync.Mutex
	results := make([]benchClip, cfg.Clips)
	for i := range results {
		results[i].Index = i
	}
	clipWG := sync.WaitGroup{}
	clipWG.Add(cfg.Clips)

	// handle runs one job through the pipeline under test.
	handle := func(j *benchJob) {
		run := kernobs.NewRunObserver(nil).StartRun(ctx, kernobs.RunInfo{
			JobID: j.jobID, AttemptID: "attempt-1",
		})
		jobCtx := kernobs.WithRun(ctx, run)

		started := time.Now()
		res, handleErr := worker.Handle(jobCtx, &job.Job{ID: j.jobID, Payload: j.payload}, nil)
		elapsed := time.Since(started)
		run.Finish()
		report := run.Report()

		switch j.kind {
		case benchJobSubmit:
			if handleErr != nil {
				recordBenchFailure(&resultsMu, results, j.index, handleErr)
				clipWG.Done()
				return
			}
			if !cfg.Async {
				finishBlockingClip(&resultsMu, results, j.index, elapsed, report, remote, j.jobID)
				clipWG.Done()
				return
			}
			// Register the parent/child pair so the real aggregator can find
			// the submitted parent and finalise it.
			childID, _ := res["child_job_id"].(string)
			raw, _ := json.Marshal(res)
			parent := &job.Job{ID: j.jobID, Type: TypeClipRender, Result: raw, Revision: 1}
			q.registerParent(j.jobID, j.index)
			jobsSvc.Register(parent, childID)
			resultsMu.Lock()
			results[j.index].Phases.PrepareMS = benchStageMS(report, kernobs.StageName(StageClipPrepare))
			results[j.index].Phases.SubtitlesMS = benchStageMS(report, kernobs.StageName(StageClipSubtitles))
			results[j.index].Phases.SubmitMS = elapsed.Milliseconds()
			resultsMu.Unlock()
		case benchJobSettle:
			status := job.StatusSucceeded
			if handleErr != nil {
				status = job.StatusFailed
			}
			jobsSvc.CompleteChild(j.childID, status)
			if cfg.EventDriven {
				// The PRODUCTION event path: the terminal child names its parent
				// (the notifier's NotifyChildTerminal → FinalizeParent), so the
				// aggregator finalises that ONE parent. No list scan, and no poll
				// interval on the latency the caller observes. In this harness the
				// submit job id IS the parent id, so the run id addresses it.
				if err := NewParentAggregator(jobsSvc, zap.NewNop(), time.Second).FinalizeParent(ctx, j.runID); err != nil {
					recordBenchFailure(&resultsMu, results, j.index, err)
				}
			}
			if handleErr != nil {
				recordBenchFailure(&resultsMu, results, j.index, handleErr)
				clipWG.Done()
				return
			}
			downloadMS := remote.downloadMSFor(j.runID)
			e2eMS := time.Since(j.enqueued).Milliseconds()
			resultsMu.Lock()
			results[j.index].Phases.SettleMS = elapsed.Milliseconds()
			results[j.index].Phases.ProbeMS = benchStageMS(report, kernobs.StageName(StageClipProbe))
			results[j.index].Phases.PublishMS = benchStageMS(report, kernobs.StageName(StageClipPublish))
			results[j.index].Phases.RenderMS = benchStageMS(report, kernobs.StageName(StageClipRender))
			results[j.index].Phases.DownloadMS = downloadMS
			// End-to-end: from the submit job being enqueued to the settle child
			// completing — the latency the caller actually observes.
			results[j.index].E2EMS = e2eMS
			resultsMu.Unlock()
			clipWG.Done()
		}
	}

	// Build one distinct source asset per clip.
	requests := make([]*RenderRequest, cfg.Clips)
	for i := 0; i < cfg.Clips; i++ {
		assetID := fmt.Sprintf("bench-source-%d", i)
		resolver.assets[assetID] = AssetRef{AssetID: assetID, DurationMS: 3000}
		req := baseRenderRequest()
		req.SourceAssetID = assetID
		requests[i] = req
	}

	// Worker pools.
	var workersWG sync.WaitGroup
	spawn := func(ch chan *benchJob) {
		workersWG.Add(1)
		go func() {
			defer workersWG.Done()
			for j := range ch {
				handle(j)
			}
		}()
	}
	for i := 0; i < cfg.Workers; i++ {
		spawn(q.submitCh)
	}
	if cfg.Async && cfg.WaiterPool > 0 {
		for i := 0; i < cfg.WaiterPool; i++ {
			spawn(q.settleCh)
		}
	}

	// Aggregator: real ticker, real finalisation logic.
	aggCtx, aggCancel := context.WithCancel(ctx)
	defer aggCancel()
	if cfg.AggregateInterval > 0 && !cfg.EventDriven {
		NewParentAggregator(jobsSvc, zap.NewNop(), cfg.AggregateInterval).Start(aggCtx)
	}

	startedAt := time.Now()

	// Enqueue the submit jobs, then seal that channel when the settle pool is
	// separate (in shared mode it must stay open: settle jobs travel on it).
	for i := 0; i < cfg.Clips; i++ {
		payload, err := json.Marshal(requests[i])
		if err != nil {
			t.Fatalf("bench: marshal request: %v", err)
		}
		jobID := fmt.Sprintf("%s-clip-%d", cfg.Scenario, i)
		results[i].RunID = jobID
		q.pushSubmit(i, jobID, payload)
	}
	q.sealSubmits()

	done := make(chan struct{})
	go func() { clipWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatalf("bench: pipeline timed out after %v", time.Since(startedAt))
	}
	// Give the aggregator ticker one full interval to observe the terminal
	// children before the run is torn down.
	if cfg.AggregateInterval > 0 && !cfg.EventDriven {
		time.Sleep(cfg.AggregateInterval + 100*time.Millisecond)
	}
	q.shutdown()
	workersWG.Wait()
	wall := time.Since(startedAt)
	aggCancel()

	// Assemble the report.
	stats := remote.stats()
	_, _, pubBytes, pubNetTX := publisher.snapshot()
	matCalls := mat.callCount()
	queueWaitByRun := q.queueWaitByRun(remote)
	parentLatency := jobsSvc.finalizedLatency(remote, q.parentsByRunID())

	report := benchReport{
		Scenario: cfg.Scenario, Mode: benchMode(cfg), Workers: cfg.Workers,
		WaiterPool: cfg.WaiterPool, GPULanes: cfg.GPULanes, Clips: cfg.Clips,
		AggregateMS: cfg.AggregateInterval.Milliseconds(), EventDriven: cfg.EventDriven,
		WallMS: wall.Milliseconds(), ClipsPerMin: benchClipsPerMin(cfg.Clips, wall),
		GPULaneBusyMS:    stats.LanesBusy.Milliseconds(),
		GPULaneUtilPct:   stats.utilization() * 100,
		GPULaneIdlePct:   (1 - stats.utilization()) * 100,
		GPUMaxConcurrent: stats.MaxConcurrent,
		RemoteQueueP95MS: remote.queueWaitP95(),
		Submits:          stats.Submits,
		Settles:          stats.Settles,
		DiskReadBytes:    int64(matCalls) * 1024,
		DiskWriteBytes:   pubBytes,
		NetworkTXBytes:   pubNetTX,
		SourceDownloads:  int64(matCalls),
		SourceFullHashes: int64(matCalls),
		StartedAt:        startedAt.UTC().Format(time.RFC3339Nano),
		FinishedAt:       time.Now().UTC().Format(time.RFC3339Nano),
		ClipRuns:         results,
	}
	report.RemoteRenderP50MS = remote.serviceP50()

	e2e := make([]int64, 0, cfg.Clips)
	submitOccupancy := make([]int64, 0, cfg.Clips)
	settleOccupancy := make([]int64, 0, cfg.Clips)
	parentMS := make([]int64, 0, cfg.Clips)
	var frames float64
	failures := 0
	for i := range results {
		c := &results[i]
		if c.Failed {
			failures++
		}
		if c.E2EMS > 0 {
			e2e = append(e2e, c.E2EMS)
		}
		c.Phases.QueueWaitMS = queueWaitByRun[c.RunID].Milliseconds()
		c.Phases.OccupiedMS = c.Phases.SubmitMS + c.Phases.SettleMS
		if c.Phases.SubmitMS > 0 {
			submitOccupancy = append(submitOccupancy, c.Phases.SubmitMS)
		}
		if c.Phases.SettleMS > 0 {
			settleOccupancy = append(settleOccupancy, c.Phases.SettleMS)
		}
		if v, ok := parentLatency[c.RunID]; ok {
			c.Phases.ParentMS = v
			parentMS = append(parentMS, v)
		}
		if !c.Failed {
			frames += 3 * 24 // duration 3 s × 24 fps
		}
	}
	report.Failures = failures
	report.P50MS = benchPercentile(e2e, 50)
	report.P95MS = benchPercentile(e2e, 95)
	report.MeanMS = benchMean(e2e)
	report.SubmitOccupancyP50MS = benchPercentile(submitOccupancy, 50)
	report.SubmitOccupancyP95MS = benchPercentile(submitOccupancy, 95)
	report.SettleOccupancyP50MS = benchPercentile(settleOccupancy, 50)
	report.SettleOccupancyP95MS = benchPercentile(settleOccupancy, 95)
	report.ParentFinalizeP50MS = benchPercentile(parentMS, 50)
	report.ParentFinalizeP95MS = benchPercentile(parentMS, 95)
	if wall > 0 {
		report.AggregatedFPS = frames / wall.Seconds()
	}
	return report
}

// benchMode labels the pipeline shape for the report.
func benchMode(cfg benchConfig) string {
	if cfg.Async {
		return "async"
	}
	return "blocking"
}

func benchStageMS(report *kernobs.RunReport, name kernobs.StageName) int64 {
	if report == nil {
		return 0
	}
	for _, st := range report.Stages {
		if st.Name == string(name) {
			return st.DurationMs
		}
	}
	return 0
}

func recordBenchFailure(mu *sync.Mutex, results []benchClip, index int, err error) {
	mu.Lock()
	defer mu.Unlock()
	results[index].Failed = true
	results[index].Error = err.Error()
}

func finishBlockingClip(mu *sync.Mutex, results []benchClip, index int, elapsed time.Duration, report *kernobs.RunReport, remote *benchRemote, runID string) {
	remote.mu.Lock()
	queueWait := remote.queueWait[runID].Milliseconds()
	remote.mu.Unlock()
	mu.Lock()
	defer mu.Unlock()
	results[index].Phases.PrepareMS = benchStageMS(report, kernobs.StageName(StageClipPrepare))
	results[index].Phases.SubtitlesMS = benchStageMS(report, kernobs.StageName(StageClipSubtitles))
	results[index].Phases.RenderMS = benchStageMS(report, kernobs.StageName(StageClipRender))
	results[index].Phases.ProbeMS = benchStageMS(report, kernobs.StageName(StageClipProbe))
	results[index].Phases.PublishMS = benchStageMS(report, kernobs.StageName(StageClipPublish))
	results[index].Phases.QueueWaitMS = queueWait
	results[index].Phases.SubmitMS = elapsed.Milliseconds()
	results[index].Phases.OccupiedMS = elapsed.Milliseconds()
	results[index].E2EMS = elapsed.Milliseconds()
}

// benchQueue owns the job accounting: submit and settle channels (the same
// channel when there is no dedicated settle pool), the run-id → clip index
// mapping the aggregator and the report need, and the e2e clock.
type benchQueue struct {
	submitCh chan *benchJob
	settleCh chan *benchJob
	separate bool

	mu           sync.Mutex
	seq          int
	parentsByRun map[string]string
	runIndex     map[string]int
	clipStart    map[string]time.Time
}

func newBenchQueue(cfg benchConfig) *benchQueue {
	capN := 2*cfg.Clips + 8
	q := &benchQueue{
		submitCh:     make(chan *benchJob, capN),
		separate:     cfg.Async && cfg.WaiterPool > 0,
		parentsByRun: map[string]string{},
		runIndex:     map[string]int{},
		clipStart:    map[string]time.Time{},
	}
	if q.separate {
		q.settleCh = make(chan *benchJob, capN)
	} else {
		q.settleCh = q.submitCh
	}
	return q
}

func (q *benchQueue) pushSubmit(index int, runID string, payload json.RawMessage) {
	now := time.Now()
	q.mu.Lock()
	q.runIndex[runID] = index
	q.clipStart[runID] = now
	q.mu.Unlock()
	q.submitCh <- &benchJob{index: index, kind: benchJobSubmit, jobID: runID, payload: payload, enqueued: now}
}

// pushSettle is the enqueue side of the continuation contract: it returns the
// child job id just like the real enqueuer and routes the settle job to the
// dedicated pool when one exists.
func (q *benchQueue) pushSettle(runID string, payload json.RawMessage) string {
	q.mu.Lock()
	q.seq++
	childID := fmt.Sprintf("bench-settle-%d", q.seq)
	index := q.runIndex[runID]
	// The clip clock starts when the submit job was enqueued, so the settle
	// phase can report the caller-visible end-to-end latency.
	clipStart := q.clipStart[runID]
	q.mu.Unlock()
	q.settleCh <- &benchJob{
		index: index, kind: benchJobSettle, jobID: childID, childID: childID,
		runID: runID, payload: payload, enqueued: clipStart,
	}
	return childID
}

func (q *benchQueue) registerParent(runID string, _ int) {
	q.mu.Lock()
	q.parentsByRun[runID] = runID
	q.mu.Unlock()
}

func (q *benchQueue) parentsByRunID() map[string]string {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make(map[string]string, len(q.parentsByRun))
	for k, v := range q.parentsByRun {
		out[k] = v
	}
	return out
}

func (q *benchQueue) queueWaitByRun(remote *benchRemote) map[string]time.Duration {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	out := make(map[string]time.Duration, len(remote.queueWait))
	for k, v := range remote.queueWait {
		out[k] = v
	}
	return out
}

// sealSubmits closes the submit channel once the settle pool is separate, so
// the submit workers can exit. In shared mode the channel must stay open.
func (q *benchQueue) sealSubmits() {
	if q.separate {
		close(q.submitCh)
	}
}

// shutdown closes every channel after all clips reached a terminal state.
func (q *benchQueue) shutdown() {
	if q.separate {
		close(q.settleCh)
		return
	}
	close(q.submitCh)
}

// benchEnqueuer adapts a closure to the ContinuationEnqueuer port.
type benchEnqueuer struct {
	fn func(Continuation) (string, error)
}

func (e benchEnqueuer) EnqueueContinuation(_ context.Context, req ContinuationRequest) (string, error) {
	return e.fn(req.Continuation)
}

func benchMax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func benchMin(a, b int) int {
	if a < b {
		return a
	}
	return b
}
