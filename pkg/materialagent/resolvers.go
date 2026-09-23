package materialagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// ── catalog resolver — the Master's already-registered material ─────────────

// CatalogResolver searches the Master's media SSOT (POST /api/media/search).
// It is the ONLY resolver that returns already-durable material, which is why
// policy puts it first: a hit here means zero acquisition cost.
type CatalogResolver struct {
	client *veloxclient.Client
}

// NewCatalogResolver builds the catalog resolver.
func NewCatalogResolver(client *veloxclient.Client) *CatalogResolver {
	return &CatalogResolver{client: client}
}

// Name implements Resolver.
func (r *CatalogResolver) Name() string { return ResolverCatalog }

// mediaSearchEnvelope is the documented POST /api/media/search response.
type mediaSearchEnvelope struct {
	Items []struct {
		AssetID    string   `json:"asset_id"`
		Source     string   `json:"source"`
		SourceRef  string   `json:"source_ref"`
		MediaType  string   `json:"media_type"`
		AssetKind  string   `json:"asset_kind"`
		Title      string   `json:"title"`
		Name       string   `json:"name"`
		SourceURL  string   `json:"source_url"`
		DurationMs int64    `json:"duration_ms"`
		Width      int      `json:"width"`
		Height     int      `json:"height"`
		Tags       []string `json:"tags"`
		Hash       string   `json:"hash"`
		Score      float64  `json:"score"`
	} `json:"items"`
	NextCursor string `json:"next_cursor"`
	Partial    bool   `json:"partial"`
}

// Search implements Resolver.
func (r *CatalogResolver) Search(ctx context.Context, req MaterialRequest) ([]Candidate, error) {
	payload := map[string]any{
		"query":    req.Description,
		"mode":     "hybrid",
		"universe": "catalog",
		"limit":    limitFor(req),
	}
	if filters := catalogFilters(req); len(filters) > 0 {
		payload["filters"] = filters
	}
	raw, err := r.client.MediaSearch(ctx, payload)
	if err != nil {
		return nil, fmt.Errorf("catalog search: %w", err)
	}
	var env mediaSearchEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("catalog search: decode response: %w", err)
	}
	out := make([]Candidate, 0, len(env.Items))
	for _, it := range env.Items {
		out = append(out, Candidate{
			Resolver:        ResolverCatalog,
			AssetID:         it.AssetID,
			Source:          it.Source,
			SourceRef:       it.SourceRef,
			MediaType:       it.MediaType,
			Title:           it.Title,
			Name:            it.Name,
			SourceURL:       it.SourceURL,
			ContentHash:     it.Hash,
			DurationSeconds: float64(it.DurationMs) / 1000.0,
			Width:           it.Width,
			Height:          it.Height,
			HasDrive:        it.AssetID != "",
			Relevance:       it.Score,
		})
	}
	return out, nil
}

// Materialize implements Resolver: a catalog hit is already durable, so there
// is nothing to acquire. Returning it as Registered is what lets the agent meet
// a request with zero downloads.
func (r *CatalogResolver) Materialize(_ context.Context, c Candidate, _ MaterialRequest) (*Material, error) {
	if c.AssetID == "" {
		return nil, fmt.Errorf("catalog materialize: candidate has no asset id")
	}
	return &Material{Candidate: c, Registered: true}, nil
}

// ── youtube resolver — live discovery + clip extraction ─────────────────────

// YouTubeResolver discovers candidate videos (GET /api/clips/search), inspects
// them (GET /api/clips/info) and extracts the useful parts
// (POST /api/clips/process with selection.mode=important unless the request
// carries explicit segments), then waits for the job.
type YouTubeResolver struct {
	client *veloxclient.Client
	// MaxSegments caps the derived important segments per extraction
	// (0 = server default).
	MaxSegments int
}

// NewYouTubeResolver builds the live-discovery resolver.
func NewYouTubeResolver(client *veloxclient.Client) *YouTubeResolver {
	return &YouTubeResolver{client: client}
}

// Name implements Resolver.
func (r *YouTubeResolver) Name() string { return ResolverYouTube }

// ProbeCaptions implements the optional captionProber capability: it reads the
// caption availability from the clip metadata endpoint. known is false when the
// endpoint answers without the field (an older server) or the probe fails, so
// the Agent's fail-closed gate can decide what to do instead of guessing.
func (r *YouTubeResolver) ProbeCaptions(ctx context.Context, c Candidate) (known, has bool) {
	if c.CaptionsKnown {
		return true, c.HasCaptions
	}
	if strings.TrimSpace(c.SourceURL) == "" {
		return false, false
	}
	meta, err := r.client.ClipInfo(ctx, c.SourceURL)
	if err != nil || meta == nil || meta.HasCaptions == nil {
		return false, false
	}
	return true, *meta.HasCaptions
}

// Search implements Resolver.
func (r *YouTubeResolver) Search(ctx context.Context, req MaterialRequest) ([]Candidate, error) {
	resp, err := r.client.SearchClipsByTopic(ctx, veloxclient.TopicSearchQuery{
		Q:     req.Description,
		Limit: limitFor(req),
	})
	if err != nil {
		return nil, fmt.Errorf("youtube search: %w", err)
	}
	out := make([]Candidate, 0, len(resp.Results))
	for _, item := range resp.Results {
		if strings.TrimSpace(item.DirectLink) == "" {
			continue
		}
		out = append(out, Candidate{
			Resolver:        ResolverYouTube,
			Source:          "youtube",
			SourceRef:       item.VideoID,
			MediaType:       "video",
			Title:           item.Title,
			SourceURL:       item.DirectLink,
			DurationSeconds: float64(item.Duration),
			Relevance:       float64(item.SimilarityScore) / 100.0,
			// Tri-state: only an explicit server answer is authoritative, so
			// a search result that predates the probe stays "unknown" and is
			// resolved later by ProbeCaptions instead of being read as "none".
			CaptionsKnown: item.HasCaptions != nil,
			HasCaptions:   item.HasCaptions != nil && *item.HasCaptions,
		})
	}
	return out, nil
}

// Materialize implements Resolver: extract the useful segments from the chosen
// video and wait for the produced artifact.
func (r *YouTubeResolver) Materialize(ctx context.Context, c Candidate, req MaterialRequest) (*Material, error) {
	if strings.TrimSpace(c.SourceURL) == "" {
		return nil, fmt.Errorf("youtube materialize: candidate has no source url")
	}

	// Pre-extraction dedup probe (T1.3): if the Master already owns this video,
	// return the registered material with 0 jobs and 0 downloads. A probe
	// FAILURE is non-fatal — extraction proceeds, and the deterministic
	// idempotency key keeps a re-submit from minting a duplicate if the video
	// was in fact already processed.
	if probe, perr := r.client.ClipExists(ctx, c.SourceURL); perr == nil && probe != nil && probe.Exists {
		m := &Material{Candidate: c, Registered: true}
		if probe.ClipID != "" {
			m.AssetID = probe.ClipID
		}
		return m, nil
	}

	// Best-effort metadata enrichment: the caller may want the canonical title
	// even though the process endpoint resolves it itself.
	if meta, err := r.client.ClipInfo(ctx, c.SourceURL); err == nil && meta != nil {
		if c.Title == "" {
			c.Title = meta.Title
		}
		if c.DurationSeconds == 0 && meta.Duration > 0 {
			c.DurationSeconds = meta.Duration
		}
	}

	payload := map[string]any{"url": c.SourceURL}
	if len(req.Segments) > 0 {
		payload["segments"] = explicitSegments(req.Segments)
	} else {
		selection := map[string]any{"mode": "important"}
		if r.MaxSegments > 0 {
			selection["max_segments"] = r.MaxSegments
		}
		payload["selection"] = selection
	}
	if dest := destinationPayload(req.Destination); dest != nil {
		payload["destination"] = dest
	}
	if req.Constraints.DurationMinSeconds > 0 || req.Constraints.DurationMaxSeconds > 0 {
		// Record the scene's intent on the job payload for operator audit; the
		// segment duration gate itself stays server-side.
		payload["segment_constraints"] = map[string]any{
			"duration_min_seconds": req.Constraints.DurationMinSeconds,
			"duration_max_seconds": req.Constraints.DurationMaxSeconds,
		}
	}

	ack, err := r.client.SubmitAsync(ctx, veloxclient.RouteClipsProcess, payload, idempotencyKey(req, c))
	if err != nil {
		return nil, fmt.Errorf("youtube materialize: submit: %w", err)
	}
	job, err := r.client.WaitJob(ctx, ack.JobID)
	if err != nil {
		return nil, fmt.Errorf("youtube materialize: job %s: %w", ack.JobID, err)
	}
	m := &Material{Candidate: c, JobID: ack.JobID}
	if id := assetIDFromResult(job.Result); id != "" {
		m.AssetID = id
		m.Registered = true
	}
	return m, nil
}

// ── stock resolver — generic b-roll acquisition ─────────────────────────────

// StockResolver acquires generic b-roll through the stock pipeline
// (POST /api/stock-pipeline/search-and-run). Unlike the catalog/YouTube
// resolvers it has no candidate list: the query IS the plan, so Search returns
// a single synthetic candidate and Materialize performs the acquisition.
type StockResolver struct {
	client *veloxclient.Client
	// MaxVideos bounds how many sources a run acquires (search runs MUST pin
	// it, otherwise the planner contract fails on a variable candidate count).
	MaxVideos int
}

// NewStockResolver builds the b-roll resolver. maxVideos should be pinned
// (>=1); 0 falls back to 1.
func NewStockResolver(client *veloxclient.Client, maxVideos int) *StockResolver {
	return &StockResolver{client: client, MaxVideos: maxVideos}
}

// Name implements Resolver.
func (r *StockResolver) Name() string { return ResolverStock }

// Search implements Resolver: the stock pipeline has no read-only candidate
// endpoint, so the request itself is the single candidate. Returning one
// candidate keeps the orchestrator uniform (score → materialize) without
// pretending a catalog exists.
func (r *StockResolver) Search(_ context.Context, req MaterialRequest) ([]Candidate, error) {
	return []Candidate{{
		Resolver:        ResolverStock,
		Source:          "stock",
		MediaType:       req.MaterialType,
		Title:           req.Description,
		Relevance:       0.5, // no ranking signal before the run; the scorer weighs freshness/durability
		DurationSeconds: req.Constraints.DurationMaxSeconds,
	}}, nil
}

// Materialize implements Resolver: run the search-and-run pipeline and wait.
func (r *StockResolver) Materialize(ctx context.Context, c Candidate, req MaterialRequest) (*Material, error) {
	maxVideos := r.MaxVideos
	if maxVideos <= 0 {
		maxVideos = 1
	}
	runReq := veloxclient.StockSearchAndRunRequest{
		Queries:   []veloxclient.StockQuery{{Q: req.Description, Limit: maxVideos * 3}},
		MaxVideos: maxVideos,
		Async:     true,
		Persist:   true,
	}
	if req.Destination != nil {
		runReq.DriveFolderID = req.Destination.FolderID
		runReq.FolderName = req.Destination.Group
		runReq.Subfolder = req.Destination.SubfolderName
	}
	ack, err := r.client.StockPipelineSearchAndRun(ctx, runReq)
	if err != nil {
		return nil, fmt.Errorf("stock materialize: submit: %w", err)
	}
	if ack.JobID == "" {
		// The endpoint can answer inline (status completed) for fixtures; there
		// is then nothing to poll and no job id to correlate.
		return &Material{Candidate: c, Registered: false}, nil
	}
	job, err := r.client.WaitJob(ctx, ack.JobID)
	if err != nil {
		return nil, fmt.Errorf("stock materialize: job %s: %w", ack.JobID, err)
	}
	m := &Material{Candidate: c, JobID: ack.JobID}
	if id := assetIDFromResult(job.Result); id != "" {
		m.AssetID = id
		m.Registered = true
	}
	return m, nil
}

// ── shared helpers ──────────────────────────────────────────────────────────

// limitFor returns the discovery page size for a request (candidates per
// source). It over-fetches a little so the scorer has room to reject.
func limitFor(req MaterialRequest) int {
	n := req.Count
	if n <= 0 {
		n = 1
	}
	limit := n * 4
	if limit < 8 {
		limit = 8
	}
	if limit > 50 {
		limit = 50
	}
	return limit
}

// catalogFilters maps the request's semantic axes onto the canonical media
// search filters.
func catalogFilters(req MaterialRequest) map[string]any {
	filters := map[string]any{}
	if req.MaterialType != "" {
		filters["media_type"] = req.MaterialType
	}
	if req.SemanticRole != "" {
		filters["semantic_role"] = req.SemanticRole
	}
	if ms := int64(req.Constraints.DurationMinSeconds * 1000); ms > 0 {
		filters["duration_ms_min"] = ms
	}
	return filters
}

// explicitSegments renders explicit clip windows into the wire shape.
func explicitSegments(segs []ClipSegment) []map[string]any {
	out := make([]map[string]any, 0, len(segs))
	for _, s := range segs {
		seg := map[string]any{"start": s.Start, "end": s.End}
		if s.Name != "" {
			seg["name"] = s.Name
		}
		out = append(out, seg)
	}
	return out
}

// destinationPayload renders the canonical nested destination object (top-level
// group/folder fields are rejected by /api/clips/process).
func destinationPayload(d *Destination) map[string]any {
	if d == nil {
		return nil
	}
	out := map[string]any{"create_subfolder": d.CreateSubfolder}
	if d.Group != "" {
		out["group"] = d.Group
	}
	if d.FolderID != "" {
		out["folder_id"] = d.FolderID
	}
	if d.FolderPath != "" {
		out["folder_path"] = d.FolderPath
	}
	if d.SubfolderName != "" {
		out["subfolder_name"] = d.SubfolderName
	}
	return out
}

// idempotencyKey makes a retry of the same scene deterministic: the same
// (project, scene, source ref) maps to the same key, so a re-submit replays
// instead of minting duplicate clips.
func idempotencyKey(req MaterialRequest, c Candidate) string {
	parts := []string{"mat", req.ProjectID, req.SceneID}
	if ref := strings.TrimSpace(c.SourceRef); ref != "" {
		parts = append(parts, ref)
	} else if parts[1] == "" && parts[2] == "" {
		parts = append(parts, req.RequestID)
	}
	return strings.Join(parts, ":")
}

// assetIDFromResult extracts the produced asset id from a completed job's
// result. The shape varies by job type, so it probes the documented keys and
// falls back to the first clip id; an unknown shape yields "" (honest) rather
// than a fabricated id.
func assetIDFromResult(result map[string]any) string {
	if result == nil {
		return ""
	}
	for _, key := range []string{"asset_id", "clip_id", "assetId"} {
		if v, ok := result[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, key := range []string{"asset_ids", "clip_ids"} {
		if list, ok := result[key].([]any); ok && len(list) > 0 {
			if v, ok := list[0].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	if items, ok := result["items"].([]any); ok && len(items) > 0 {
		if first, ok := items[0].(map[string]any); ok {
			for _, key := range []string{"asset_id", "clip_id", "id"} {
				if v, ok := first[key].(string); ok && strings.TrimSpace(v) != "" {
					return strings.TrimSpace(v)
				}
			}
		}
	}
	return ""
}
