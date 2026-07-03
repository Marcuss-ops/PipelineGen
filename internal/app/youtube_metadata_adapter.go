// Package app — sourcing utility adapters extracted from
// assets_register_adapters.go (PR-GODOBJ-8, July 2026).
//
// Contains: sourcingMetadataAdapter, sourcingEnrichmentAdapter,
// sourcingConfigAdapter, sourcingTranscriberAdapter,
// sourcingSearchAdapter, sourcingHashAdapter, zapSourcingLogger.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	clipsapi "github.com/Marcuss-ops/PipelineGen/internal/api/assets/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/providers"
	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	appclips "github.com/Marcuss-ops/PipelineGen/internal/application/clips"
	"github.com/Marcuss-ops/PipelineGen/internal/domain/asset"
	driveutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/drive"
	hashutil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/files"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── sourcingMetadataAdapter ───────────────────────────────────────────

type sourcingMetadataAdapter struct {
	cfg    *config.Config
	admin  driveutil.Admin
	reader driveutil.Reader
	log    *zap.Logger
}

func (a *sourcingMetadataAdapter) UpdateCumulativeJSON(ctx context.Context, tempDir, folderID, clipID string, entry map[string]any) error {
	if a.admin == nil || a.cfg == nil {
		return nil
	}
	appclips.UpdateCumulativeMetadataJSON(ctx, newClipsDriveAdapter(a.admin, a.reader), a.cfg.Storage.TempPath(), folderID, clipID, entry, a.log)
	return nil
}

// ── zapSourcingLogger ─────────────────────────────────────────────────

type zapSourcingLogger struct {
	log *zap.Logger
}

func (a *zapSourcingLogger) Info(msg string, keysAndValues ...any) {
	a.log.Sugar().Infow(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Warn(msg string, keysAndValues ...any) {
	a.log.Sugar().Warnw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Error(msg string, keysAndValues ...any) {
	a.log.Sugar().Errorw(msg, keysAndValues...)
}
func (a *zapSourcingLogger) Debug(msg string, keysAndValues ...any) {
	a.log.Sugar().Debugw(msg, keysAndValues...)
}

// ── sourcingEnrichmentAdapter ─────────────────────────────────────────

type sourcingEnrichmentAdapter struct {
	handler *clipsapi.Handler
}

func (a *sourcingEnrichmentAdapter) EnrichAndIndex(ctx context.Context, clipID, localPath, source string) error {
	if a.handler == nil {
		return nil
	}
	clip := &asset.Asset{
		ID:        clipID,
		Source:    asset.Source(source),
		Name:      clipID,
		MediaType: asset.MediaType("video"),
	}
	clip.SetLocalPath(localPath)
	a.handler.EnrichAndIndexClip(ctx, clip, source)
	return nil
}

// ── sourcingConfigAdapter ─────────────────────────────────────────────

type sourcingConfigAdapter struct {
	cfg *config.Config
}

func (a *sourcingConfigAdapter) ClipsFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *sourcingConfigAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

// ── sourcingTranscriberAdapter ────────────────────────────────────────

type sourcingTranscriberAdapter struct {
	cfg *config.Config
	log *zap.Logger
}

func (a *sourcingTranscriberAdapter) Transcribe(ctx context.Context, audioPath string) (string, string, error) {
	if a.cfg == nil {
		return "", "", fmt.Errorf("register transcriber config not configured")
	}
	if audioPath == "" {
		return "", "", nil
	}
	if _, err := executil.LookPath("python3"); err != nil {
		return "", "", err
	}
	scriptPath := filepath.Join(a.cfg.Paths.PythonScriptsDir, "tools", "transcribe_detect_lang.py")
	if _, err := os.Stat(scriptPath); err != nil {
		return "", "", err
	}
	res, err := executil.RunSimple(ctx, "python3", scriptPath, "--transcribe", "--model", "tiny", "--json-only", audioPath)
	if err != nil {
		return "", "", err
	}
	type transcriptResult struct {
		Language       string `json:"language"`
		TranscriptFull string `json:"transcript_full"`
		Error          string `json:"error"`
	}
	var parsed transcriptResult
	if err := json.Unmarshal([]byte(res.Output), &parsed); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return "", "", fmt.Errorf("%s", parsed.Error)
	}
	return strings.TrimSpace(parsed.TranscriptFull), strings.TrimSpace(parsed.Language), nil
}

// ── sourcingSearchAdapter ─────────────────────────────────────────────

type sourcingSearchAdapter struct {
	registry *providers.Registry
}

func (a *sourcingSearchAdapter) Search(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
	if a.registry == nil {
		return nil, nil
	}
	var out []sourcing.SearchCandidate
	for _, p := range a.registry.ByCapability(providers.CapabilitySearch) {
		sp, ok := p.(providers.SearchProvider)
		if !ok {
			continue
		}
		res, err := sp.Search(ctx, providers.SearchRequest{Query: query, Limit: limit})
		if err != nil {
			continue
		}
		for _, cand := range res.Candidates {
			out = append(out, sourcing.SearchCandidate{
				SourceRef: cand.SourceRef,
				Title:     cand.Title,
				Score:     cand.Score,
			})
		}
	}
	return out, nil
}

// ── sourcingHashAdapter ───────────────────────────────────────────────

type sourcingHashAdapter struct{}

func (a *sourcingHashAdapter) MD5File(path string) (string, error) {
	return hashutil.MD5File(path)
}

var _ sourcing.HashPort = (*sourcingHashAdapter)(nil)
