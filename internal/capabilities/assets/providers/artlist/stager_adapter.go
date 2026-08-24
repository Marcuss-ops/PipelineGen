package assets

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
)

var _ acquisition.SourceStager = (*ArtlistStager)(nil)

var (
	ErrArtlistStagerNotWired     = errors.New("artlist stager: downloader not wired")
	ErrArtlistStagerEmptyURL     = errors.New("artlist stager: empty URL")
	ErrArtlistStagerNoStagedFile = errors.New("artlist stager: no staged file produced")
)

type ArtlistStager struct {
	downloader Downloader
	mu         sync.Mutex
	receipts   map[string]acquisition.PrepareContext
	released   map[string]struct{}
}

func NewArtlistStager(downloader Downloader) *ArtlistStager {
	return &ArtlistStager{downloader: downloader, receipts: make(map[string]acquisition.PrepareContext), released: make(map[string]struct{})}
}

func (s *ArtlistStager) Prepare(ctx context.Context, req acquisition.PrepareRequest) (*acquisition.PrepareContext, error) {
	if s == nil || s.downloader == nil {
		return nil, ErrArtlistStagerNotWired
	}
	if req.Source.URL == "" {
		return nil, ErrArtlistStagerEmptyURL
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

	filename := filepath.Base(req.Source.URL)
	if filename == "" || filename == "." || filename == string(filepath.Separator) {
		filename = "artlist_asset"
	}
	stageDir := filepath.Join(os.TempDir(), "pipelinegen-artlist-staging", token[:16])
	result, err := s.downloader.Download(ctx, DownloadRequest{
		SourceRef:     req.Source.URL,
		DestinationID: stageDir,
		Filename:      filename,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: artlist stager download %q: %v", acquisition.ErrAcquisitionPrepareFailed, req.Source.URL, err)
	}
	if result == nil || result.LocalPath == "" {
		return nil, fmt.Errorf("%w: url=%q", ErrArtlistStagerNoStagedFile, req.Source.URL)
	}
	info, err := os.Stat(result.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("%w: artlist stager stat %q: %v", acquisition.ErrAcquisitionPrepareFailed, result.LocalPath, err)
	}
	hash, err := hashFile(result.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("%w: artlist stager hash %q: %v", acquisition.ErrAcquisitionPrepareFailed, result.LocalPath, err)
	}
	receipt := acquisition.PrepareContext{
		ID:           acquisition.DeriveStageID(req.Source),
		SourceRef:    req.Source,
		LocalPath:    result.LocalPath,
		SHA256:       hash,
		SizeBytes:    info.Size(),
		MIMEType:     req.Source.MIMETypeHint,
		ExpiresAt:    time.Now().UTC().Add(req.TTL),
		CleanupToken: token,
	}
	s.receipts[token] = receipt
	delete(s.released, token)
	return &receipt, nil
}

func (s *ArtlistStager) Release(_ context.Context, cleanupToken string) error {
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
	stageDir := filepath.Dir(receipt.LocalPath)
	if err := os.RemoveAll(stageDir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("artlist stager: release %q: %w", receipt.LocalPath, err)
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
