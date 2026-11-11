package stock

import (
	"context"
	"strings"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
	scriptpkg "github.com/Marcuss-ops/PipelineGen/internal/kernel/script"
	"go.uber.org/zap"
)

const maxScriptPrefetchBindings = 50

type scriptPrefetcher struct {
	service *stockpipeline.Service
	log     *zap.Logger
}

// NewScriptPrefetcher adapts Stock source-cache warming to the script
// capability port. URL selection and its bound belong to the Stock subsystem;
// script generation only receives the narrow prefetch capability.
func NewScriptPrefetcher(service *stockpipeline.Service, log *zap.Logger) scriptports.StockPrefetcher {
	if service == nil {
		return nil
	}
	return &scriptPrefetcher{service: service, log: log}
}

func (p *scriptPrefetcher) Prefetch(ctx context.Context, bindings []scriptpkg.StockBindingInput) scriptports.StockPrefetchReport {
	report := scriptports.StockPrefetchReport{}
	if p == nil || p.service == nil {
		report.Skipped = len(bindings)
		return report
	}
	urls, skipped := scriptPrefetchURLs(bindings)
	report.Skipped = skipped
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

// scriptPrefetchURLs only prewarms individual files. A folder binding is a
// remote stock selection contract for the final renderer; opening or caching
// its contents on the script worker would process the stock locally.
func scriptPrefetchURLs(bindings []scriptpkg.StockBindingInput) ([]string, int) {
	seen := make(map[string]struct{}, len(bindings))
	urls := make([]string, 0, len(bindings))
	skipped := 0
	for _, binding := range bindings {
		if strings.TrimSpace(binding.FolderID) != "" || strings.TrimSpace(binding.FolderLink) != "" {
			skipped++
			continue
		}
		if len(urls) >= maxScriptPrefetchBindings {
			skipped++
			continue
		}
		url := strings.TrimSpace(binding.DriveLink)
		if url == "" && strings.EqualFold(strings.TrimSpace(binding.Source), "youtube") && strings.TrimSpace(binding.AssetID) != "" {
			url = "https://drive.google.com/file/d/" + strings.TrimSpace(binding.AssetID) + "/view"
		}
		if url == "" {
			skipped++
			continue
		}
		if _, ok := seen[url]; ok {
			skipped++
			continue
		}
		seen[url] = struct{}{}
		urls = append(urls, url)
	}
	return urls, skipped
}
