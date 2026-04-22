package stockjit

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"velox/go-master/internal/artlistdb"
	"velox/go-master/internal/clipsearch"
	"velox/go-master/internal/stockdb"
	"velox/go-master/internal/upload/drive"
	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

const (
	defaultJITTimeout   = 3 * time.Second
	defaultRecentWindow = 24 * time.Hour
	jitStateFileName    = "jit_requests.json"
)

type Request struct {
	Topic     string
	Phrase    string
	Keywords  []string
	StartTime int
	EndTime   int
	Duration  int
	MediaType string
	AllowJIT  bool
}

type Result struct {
	RequestID      string  `json:"request_id"`
	Topic          string  `json:"topic"`
	Phrase         string  `json:"phrase"`
	Keyword        string  `json:"keyword"`
	Source         string  `json:"source"`
	SourceKind     string  `json:"source_kind"`
	Confidence     float64 `json:"confidence"`
	StartTime      int     `json:"start_time"`
	EndTime        int     `json:"end_time"`
	DriveID        string  `json:"drive_id"`
	DriveURL       string  `json:"drive_url"`
	FolderID       string  `json:"folder_id"`
	FolderPath     string  `json:"folder_path"`
	Filename       string  `json:"filename"`
	ApprovedBy     string  `json:"approved_by,omitempty"`
	ApprovalReason string  `json:"approval_reason,omitempty"`
	Cached         bool    `json:"cached,omitempty"`
}

type YouTubeCandidate struct {
	VideoID     string
	VideoURL    string
	Title       string
	Channel     string
	Uploader    string
	ViewCount   int64
	DurationSec float64
	UploadDate  string
	Description string
	Relevance   int
}

type Approver interface {
	Generate(ctx context.Context, prompt string) (string, error)
}

type ClipSearcher interface {
	RankedYouTubeCandidates(ctx context.Context, keyword string) ([]*clipsearch.YouTubeClipMetadata, error)
	DownloadYouTubeCandidate(ctx context.Context, keyword string, candidate *clipsearch.YouTubeClipMetadata) (string, *clipsearch.YouTubeClipMetadata, error)
	ProcessDownloadedYouTubeMomentsToFolder(ctx context.Context, keyword, rawPath string, baseMeta *clipsearch.YouTubeClipMetadata, folderID string) ([]clipsearch.SearchResult, int, error)
}

type StateStore struct {
	path string
	mu   sync.Mutex
	data map[string]*stateEntry
}

type stateEntry struct {
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
	Result    *Result   `json:"result,omitempty"`
}

func NewStateStore(dataDir string) *StateStore {
	if strings.TrimSpace(dataDir) == "" {
		dataDir = "./data"
	}
	return &StateStore{
		path: filepath.Join(dataDir, jitStateFileName),
		data: make(map[string]*stateEntry),
	}
}

func (s *StateStore) Load() error {
	if s == nil || s.path == "" {
		return nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var decoded map[string]*stateEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	s.mu.Lock()
	s.data = decoded
	s.mu.Unlock()
	return nil
}

func (s *StateStore) Save() error {
	if s == nil || s.path == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

func (s *StateStore) Get(key string) (*stateEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok {
		return nil, false
	}
	copy := *e
	return &copy, true
}

func (s *StateStore) Put(key string, entry *stateEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.data[key] = entry
	s.mu.Unlock()
	_ = s.Save()
}

type Resolver struct {
	clipSearch ClipSearcher
	approver   Approver
	stockDB    *stockdb.StockDB
	artlistDB  *artlistdb.ArtlistDB
	drive      *drive.Client
	dataDir    string
	state      *StateStore
}

func NewResolver(clipSearch ClipSearcher, approver Approver, stockDB *stockdb.StockDB, artlistDB *artlistdb.ArtlistDB, driveClient *drive.Client, dataDir string) *Resolver {
	state := NewStateStore(dataDir)
	if err := state.Load(); err != nil && !os.IsNotExist(err) {
		logger.Warn("Failed to load JIT state store", zap.Error(err))
	}
	return &Resolver{
		clipSearch: clipSearch,
		approver:   approver,
		stockDB:    stockDB,
		artlistDB:  artlistDB,
		drive:      driveClient,
		dataDir:    dataDir,
		state:      state,
	}
}

func (r *Resolver) Resolve(ctx context.Context, req Request) (*Result, error) {
	if r == nil || !req.AllowJIT {
		return nil, nil
	}
	key := requestKey(req)
	if cached, ok := r.state.Get(key); ok && cached != nil && cached.Status == "done" && time.Since(cached.UpdatedAt) <= defaultRecentWindow {
		if cached.Result != nil {
			copy := *cached.Result
			copy.Cached = true
			return &copy, nil
		}
	}

	if res := r.tryStockDB(req); res != nil {
		r.state.Put(key, &stateEntry{Status: "done", UpdatedAt: time.Now(), Result: res})
		return res, nil
	}
	if res := r.tryArtlistDB(req); res != nil {
		r.state.Put(key, &stateEntry{Status: "done", UpdatedAt: time.Now(), Result: res})
		return res, nil
	}
	if r.clipSearch == nil || r.approver == nil || r.drive == nil {
		return nil, nil
	}

	candidates, err := r.clipSearch.RankedYouTubeCandidates(ctx, req.Keyword())
	if err != nil || len(candidates) == 0 {
		r.state.Put(key, &stateEntry{Status: "failed", UpdatedAt: time.Now()})
		return nil, err
	}
	approved, approvalReason, err := r.approveCandidates(ctx, req, candidates)
	if err != nil || len(approved) == 0 {
		r.state.Put(key, &stateEntry{Status: "failed", UpdatedAt: time.Now()})
		return nil, err
	}

	folderID, folderPath, err := r.resolveFolder(ctx, req.Topic, req.Keyword())
	if err != nil {
		r.state.Put(key, &stateEntry{Status: "failed", UpdatedAt: time.Now()})
		return nil, err
	}

	for _, cand := range approved {
		rawPath, meta, dlErr := r.clipSearch.DownloadYouTubeCandidate(ctx, req.Keyword(), cand)
		if dlErr != nil {
			logger.Warn("JIT download failed", zap.String("keyword", req.Keyword()), zap.String("video_id", cand.VideoID), zap.Error(dlErr))
			continue
		}
		processed, _, procErr := r.clipSearch.ProcessDownloadedYouTubeMomentsToFolder(ctx, req.Keyword(), rawPath, meta, folderID)
		_ = os.Remove(rawPath)
		if procErr != nil || len(processed) == 0 {
			logger.Warn("JIT processing failed", zap.String("keyword", req.Keyword()), zap.String("video_id", cand.VideoID), zap.Error(procErr))
			continue
		}
		clip := processed[0]
		result := &Result{
			RequestID:      key,
			Topic:          req.Topic,
			Phrase:         req.Phrase,
			Keyword:        req.Keyword(),
			Source:         "youtube",
			SourceKind:     "jit_stock",
			Confidence:     0.95,
			StartTime:      req.StartTime,
			EndTime:        req.EndTime,
			DriveID:        clip.DriveID,
			DriveURL:       clip.DriveURL,
			FolderID:       clip.FolderID,
			FolderPath:     folderPath,
			Filename:       clip.Filename,
			ApprovedBy:     "gemma",
			ApprovalReason: approvalReason,
		}
		if r.stockDB != nil {
			_ = r.stockDB.UpsertClip(stockdb.StockClipEntry{
				ClipID:   clip.DriveID,
				FolderID: clip.FolderID,
				Filename: clip.Filename,
				Source:   "jit_stock",
				Tags:     req.Tags(),
				Duration: req.Duration,
				Status:   "uploaded",
			})
		}
		r.state.Put(key, &stateEntry{Status: "done", UpdatedAt: time.Now(), Result: result})
		return result, nil
	}

	r.state.Put(key, &stateEntry{Status: "failed", UpdatedAt: time.Now()})
	return nil, nil
}

func (r *Resolver) tryStockDB(req Request) *Result {
	if r.stockDB == nil {
		return nil
	}
	tags := req.Tags()
	if len(tags) == 0 {
		return nil
	}
	clips, err := r.stockDB.SearchClipsByTagsInSection(tags, "stock")
	if err != nil || len(clips) == 0 {
		return nil
	}
	c := clips[0]
	return &Result{
		RequestID:  requestKey(req),
		Topic:      req.Topic,
		Phrase:     req.Phrase,
		Keyword:    req.Keyword(),
		Source:     "stock_db",
		SourceKind: "stock",
		Confidence: 0.88,
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
		DriveID:    c.ClipID,
		FolderID:   c.FolderID,
		Filename:   c.Filename,
		FolderPath: req.Topic,
	}
}

func (r *Resolver) tryArtlistDB(req Request) *Result {
	if r.artlistDB == nil {
		return nil
	}
	tags := req.Tags()
	if len(tags) == 0 {
		return nil
	}
	clips, err := r.artlistDB.FindDownloadedClipsWithSimilarTags(tags, 1)
	if err != nil || len(clips) == 0 {
		return nil
	}
	c := clips[0]
	return &Result{
		RequestID:  requestKey(req),
		Topic:      req.Topic,
		Phrase:     req.Phrase,
		Keyword:    req.Keyword(),
		Source:     "artlist",
		SourceKind: "artlist",
		Confidence: 0.8,
		StartTime:  req.StartTime,
		EndTime:    req.EndTime,
		DriveID:    c.DriveFileID,
		DriveURL:   c.DriveURL,
		FolderID:   c.FolderID,
		FolderPath: c.Folder,
		Filename:   c.Name,
	}
}

func (r *Resolver) approveCandidates(ctx context.Context, req Request, candidates []*clipsearch.YouTubeClipMetadata) ([]*clipsearch.YouTubeClipMetadata, string, error) {
	top := candidates
	if len(top) > 5 {
		top = top[:5]
	}
	var b strings.Builder
	b.WriteString("Sei Gemma. Approva solo video veramente adatti a stock footage.\n")
	b.WriteString("Rispondi SOLO JSON: {\"approved\": [\"video_id1\"], \"rejected\": [\"video_id2\"], \"reason\": \"...\"}\n\n")
	b.WriteString("TOPIC: " + req.Topic + "\n")
	b.WriteString("PHRASE: " + req.Phrase + "\n")
	b.WriteString("KEYWORDS: " + strings.Join(req.Tags(), ", ") + "\n\n")
	for i, c := range top {
		b.WriteString(fmt.Sprintf("%d. video_id=%s title=%s views=%d duration=%.0f description=%s\n", i+1, c.VideoID, c.Title, c.ViewCount, c.DurationSec, truncate(c.Description, 220)))
	}

	ctx, cancel := context.WithTimeout(ctx, defaultJITTimeout)
	defer cancel()
	raw, err := r.approver.Generate(ctx, b.String())
	if err != nil {
		return nil, "", err
	}
	var resp struct {
		Approved []string `json:"approved"`
		Rejected []string `json:"rejected"`
		Reason   string   `json:"reason"`
	}
	jsonStr := extractJSON(raw)
	if err := json.Unmarshal([]byte(jsonStr), &resp); err != nil {
		return nil, "", err
	}
	allowed := make([]*clipsearch.YouTubeClipMetadata, 0, len(resp.Approved))
	allowedSet := make(map[string]bool)
	for _, id := range resp.Approved {
		allowedSet[strings.TrimSpace(id)] = true
	}
	for _, c := range top {
		if allowedSet[c.VideoID] {
			allowed = append(allowed, c)
		}
	}
	if len(allowed) == 0 && len(top) > 0 {
		allowed = append(allowed, top[0])
	}
	return allowed, resp.Reason, nil
}

func (r *Resolver) resolveFolder(ctx context.Context, topic, keyword string) (string, string, error) {
	if r.drive == nil {
		return "", "", fmt.Errorf("drive client not available")
	}
	root, err := r.drive.GetOrCreateFolder(ctx, "Stock", "root")
	if err != nil {
		return "", "", err
	}
	topicFolder, err := r.drive.GetOrCreateFolder(ctx, sanitizeFolderName(topic), root)
	if err != nil {
		return "", "", err
	}
	keywordFolder, err := r.drive.GetOrCreateFolder(ctx, sanitizeFolderName(keyword), topicFolder)
	if err != nil {
		return "", "", err
	}
	return keywordFolder, fmt.Sprintf("Stock/%s/%s", sanitizeFolderName(topic), sanitizeFolderName(keyword)), nil
}

func (req Request) Keyword() string {
	if len(req.Keywords) > 0 {
		return req.Keywords[0]
	}
	if strings.TrimSpace(req.Phrase) != "" {
		return req.Phrase
	}
	return req.Topic
}

func (req Request) Tags() []string {
	parts := append([]string{}, req.Keywords...)
	parts = append(parts, tokenize(req.Phrase)...)
	parts = append(parts, tokenize(req.Topic)...)
	seen := make(map[string]bool)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if len(p) < 2 || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

func requestKey(req Request) string {
	sum := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(req.Topic)) + "|" + strings.ToLower(strings.TrimSpace(req.Phrase)) + "|" + strings.Join(req.Tags(), ",") + "|" + fmt.Sprintf("%d:%d:%d", req.StartTime, req.EndTime, req.Duration) + "|" + strings.ToLower(strings.TrimSpace(req.MediaType))))
	return hex.EncodeToString(sum[:])
}

func extractJSON(raw string) string {
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "```") {
		re := regexp.MustCompile("(?s)```(?:json)?\\s*(.*?)\\s*```")
		if m := re.FindStringSubmatch(raw); len(m) > 1 {
			raw = m[1]
		}
	}
	return raw
}

func tokenize(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.ReplaceAll(s, "_", " ")
	s = strings.ReplaceAll(s, "-", " ")
	fields := strings.Fields(s)
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if len(f) < 3 {
			continue
		}
		out = append(out, f)
	}
	return out
}

func sanitizeFolderName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Unknown"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '-':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "Unknown"
	}
	return out
}

func truncate(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max]
}
