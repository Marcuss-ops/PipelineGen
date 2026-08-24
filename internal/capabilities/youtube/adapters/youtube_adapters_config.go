// Package app — sourcing config + transcriber + search adapters
// split from youtube_metadata_adapter.go (PR-GODOBJ-Azione-4, July 2026).
//
// 3 adapters: SourcingConfigAdapter, SourcingTranscriberAdapter, SourcingSearchAdapter.
package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"

	"github.com/Marcuss-ops/PipelineGen/internal/application/assets/sourcing"
	"github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers"
	executil "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/process"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/config"
)

// ── SourcingConfigAdapter ─────────────────────────────────────────────

type SourcingConfigAdapter struct {
	cfg *config.Config
}

func (a *SourcingConfigAdapter) ClipsFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.ClipsFolder()
}

func (a *SourcingConfigAdapter) RootFolder() string {
	if a.cfg == nil {
		return ""
	}
	return a.cfg.Drive.RootFolder()
}

// ── SourcingTranscriberAdapter ────────────────────────────────────────

type SourcingTranscriberAdapter struct {
	cfg *config.Config
	log *zap.Logger
}

func (a *SourcingTranscriberAdapter) Transcribe(ctx context.Context, audioPath string) (string, string, error) {
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
	res, err := executil.Run(ctx, "python3", []string{scriptPath, "--transcribe", "--model", "tiny", "--json-only", audioPath}, executil.Options{CombinedOutput: false})
	if err != nil {
		return "", "", err
	}
	type transcriptResult struct {
		Language       string `json:"language"`
		TranscriptFull string `json:"transcript_full"`
		Error          string `json:"error"`
	}
	var parsed transcriptResult
	if err := json.Unmarshal([]byte(res.Stdout), &parsed); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(parsed.Error) != "" {
		return "", "", fmt.Errorf("%s", parsed.Error)
	}
	return strings.TrimSpace(parsed.TranscriptFull), strings.TrimSpace(parsed.Language), nil
}

// ── SourcingSearchAdapter ─────────────────────────────────────────────

type SourcingSearchAdapter struct {
	registry *providers.Registry
}

func (a *SourcingSearchAdapter) Search(ctx context.Context, query string, limit int) ([]sourcing.SearchCandidate, error) {
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
