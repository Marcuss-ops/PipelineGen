package youtube

import (
	"context"
	"errors"
	"fmt"
	"github.com/Marcuss-ops/PipelineGen/internal/kernel/digest"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/application/acquisition"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
)

var _ acquisition.SourceStager = (*YouTubeStager)(nil)

var (
	ErrYoutubeStagerNotWired     = errors.New("youtube stager: adapter not wired")
	ErrYoutubeStagerEmptyURL     = errors.New("youtube stager: empty URL")
	ErrYoutubeStagerNoStagedFile = errors.New("youtube stager: no staged file produced")
)

type YouTubeStager struct {
	adapter  *Adapter
	mu       sync.Mutex
	receipts map[string]acquisition.PrepareContext
	released map[string]struct{}
}

func NewYouTubeStager(adapter *Adapter) *YouTubeStager {
	return &YouTubeStager{adapter: adapter, receipts: make(map[string]acquisition.PrepareContext), released: make(map[string]struct{})}
}

func (s *YouTubeStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	if s == nil || s.adapter == nil {
		return nil, ErrYoutubeStagerNotWired
	}
	if req.Source.URL == "" {
		return nil, ErrYoutubeStagerEmptyURL
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = acquisition.DeriveIdempotencyKey(req.Source)
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if req.Timeout == 0 {
		req.Timeout = 10 * time.Minute
	}
	if req.TTL == 0 {
		req.TTL = 24 * time.Hour
	}

	token := acquisition.DeriveCleanupToken(req.Source)
	s.mu.Lock()
	defer s.mu.Unlock()
	if receipt, ok := s.receipts[token]; ok && !receipt.Expired() {
		copy := receipt
		return &copy, nil
	}

	safeName := filepath.Base(req.Source.URL)
	if safeName == "" || safeName == "." || safeName == "/" {
		safeName = "youtube_source"
	}
	fetchReq := providers.FetchRequest{SourceRef: req.Source.URL, AssetID: safeName}
	if req.Source.DownloadSection != "" {
		start, end, err := parseDownloadSection(req.Source.DownloadSection)
		if err != nil {
			return nil, fmt.Errorf("youtube stager: parse download section %q: %w", req.Source.DownloadSection, err)
		}
		fetchReq.SegmentStart = start
		fetchReq.SegmentEnd = end
	}
	fetched, err := s.adapter.Fetch(ctx, fetchReq)
	if err != nil {
		return nil, fmt.Errorf("%w: youtube stager fetch %q: %v", acquisition.ErrAcquisitionPrepareFailed, req.Source.URL, err)
	}
	if fetched == nil || fetched.LocalPath == "" {
		return nil, fmt.Errorf("%w: url=%q", ErrYoutubeStagerNoStagedFile, req.Source.URL)
	}

	info, err := os.Stat(fetched.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("%w: youtube stager stat %q: %v", acquisition.ErrAcquisitionPrepareFailed, fetched.LocalPath, err)
	}
	hash, err := hashFile(fetched.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("%w: youtube stager hash %q: %v", acquisition.ErrAcquisitionPrepareFailed, fetched.LocalPath, err)
	}
	receipt := acquisition.PrepareContext{
		ID:           acquisition.DeriveStageID(req.Source),
		SourceRef:    req.Source,
		LocalPath:    fetched.LocalPath,
		SHA256:       hash,
		SizeBytes:    info.Size(),
		MIMEType:     req.Source.MIMETypeHint,
		ExpiresAt:    nowUTC().Add(req.TTL),
		CleanupToken: token,
	}
	s.receipts[token] = receipt
	delete(s.released, token)
	return &receipt, nil
}

func (s *YouTubeStager) Release(_ context.Context, cleanupToken string) error {
	if s == nil {
		return acquisition.ErrAcquisitionNotWired
	}
	if cleanupToken == "" {
		return acquisition.ErrAcquisitionInvalidToken
	}
	s.mu.Lock()
	receipt, ok := s.receipts[cleanupToken]
	if ok {
		delete(s.receipts, cleanupToken)
		s.released[cleanupToken] = struct{}{}
	} else {
		_, wasReleased := s.released[cleanupToken]
		s.mu.Unlock()
		if wasReleased {
			return acquisition.ErrAcquisitionAlreadyReleased
		}
		return acquisition.ErrAcquisitionInvalidToken
	}
	s.mu.Unlock()
	if err := os.Remove(receipt.LocalPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("youtube stager: release %q: %w", receipt.LocalPath, err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := digest.SHA256Bytes(data)
	return sum, nil
}

func nowUTC() time.Time { return time.Now().UTC() }
