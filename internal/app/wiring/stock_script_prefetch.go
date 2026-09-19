package wiring

import (
	"context"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

const maxScriptStockPrefetchBindings = 50

// stockScriptPrefetcher adapts the stock cache warmer to the
// script.generate payload. The script layer never imports Drive or stock
// infrastructure; it only receives this narrow capability port.
type stockScriptPrefetcher struct {
	service *stockpipeline.Service
	log     *zap.Logger
}

func newStockScriptPrefetcher(service *stockpipeline.Service, log *zap.Logger) scriptports.StockPrefetcher {
	if service == nil {
		return nil
	}
	return &stockScriptPrefetcher{service: service, log: log}
}

func (p *stockScriptPrefetcher) Prefetch(ctx context.Context, bindings []scriptpkg.StockBindingInput) scriptports.StockPrefetchReport {
	report := scriptports.StockPrefetchReport{}
	if p == nil || p.service == nil {
		report.Skipped = len(bindings)
		return report
	}
	seen := make(map[string]struct{}, len(bindings))
	urls := make([]string, 0, len(bindings))
	for _, binding := range bindings {
		if len(urls) >= maxScriptStockPrefetchBindings {
			report.Skipped++
			continue
		}
		url := strings.TrimSpace(binding.DriveLink)
		if url == "" {
			url = strings.TrimSpace(binding.FolderLink)
		}
		if url == "" && strings.TrimSpace(binding.FolderID) != "" {
			url = "https://drive.google.com/drive/folders/" + strings.TrimSpace(binding.FolderID)
		}
		if url == "" && strings.EqualFold(strings.TrimSpace(binding.Source), "youtube") && strings.TrimSpace(binding.AssetID) != "" {
			url = "https://drive.google.com/file/d/" + strings.TrimSpace(binding.AssetID) + "/view"
		}
		if url == "" {
			report.Skipped++
			continue
		}
		if _, ok := seen[url]; ok {
			report.Skipped++
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	report.Requested = len(urls)
	if len(urls) == 0 {
		return report
	}
	warm := p.service.WarmSourceCache(ctx, urls)
	report.Warmed = warm.Warmed
	report.Cached = warm.AlreadyCached
	report.Failed = warm.Failed
	if p.log != nil {
		p.log.Info("script.generate: stock bindings pre-baked",
			zap.Int("requested", report.Requested),
			zap.Int("warmed", report.Warmed),
			zap.Int("cached", report.Cached),
			zap.Int("failed", report.Failed),
			zap.Int("skipped", report.Skipped))
	}
	return report
}
