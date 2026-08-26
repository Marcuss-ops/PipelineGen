// Package retrieved (application/images/retrieved) — provider_wikipedia.go
// holds the WikipediaProvider concrete implementation. Per PR-IMG-SPLIT-3
// (July 2026), each concrete provider lives in its own file.
//
// WikipediaProvider searches the Wikimedia API for an exact or fuzzy
// match and returns the page-image URL (original preferred, thumbnail
// fallback). License defaults to CC-BY-SA-4.0 with "Wikipedia
// Contributors" as author.
package retrieved

import (
	detail "github.com/Marcuss-ops/PipelineGen/internal/kernel/asset/detail"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/kernel/asset"
	"go.uber.org/zap"
	nethttp "net/http"
)

// WikipediaProvider searches the Wikimedia API for an exact or fuzzy
// match and returns the page-image URL (original preferred,
// thumbnail fallback).
type WikipediaProvider struct {
	bridge   StorageBridge
	client   httpDoer
	log      *zap.Logger
	baseHost string // e.g. "en.wikipedia.org"
}

// NewWikipediaProvider constructs a WikipediaProvider wired to the
// parent ImageStorageService via StorageBridge. lang tag defaults to
// "en" when empty.
func NewWikipediaProvider(bridge StorageBridge, client httpDoer, log *zap.Logger, lang string) *WikipediaProvider {
	if client == nil {
		client = &nethttp.Client{Timeout: 10 * time.Second}
	}
	if lang == "" {
		lang = "en"
	}
	return &WikipediaProvider{
		bridge:   bridge,
		client:   client,
		log:      log,
		baseHost: lang + ".wikipedia.org",
	}
}

func (p *WikipediaProvider) Name() detail.ImageProvider { return detail.ProviderWikipedia }

func (p *WikipediaProvider) Healthy(ctx context.Context) error {
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, _ := nethttp.NewRequestWithContext(probeCtx, "GET", "https://"+p.baseHost+"/w/api.php?action=query&format=json", nil)
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("wikipedia unreachable: %w", err)
	}
	resp.Body.Close()
	return nil
}

func (p *WikipediaProvider) Search(ctx context.Context, query string, opts RetrievalSearchOptions) ([]RetrievalSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	lang := opts.Lang
	if lang == "" {
		lang = "en"
	}
	// baseHost reflects the requested lang so per-call overrides work.
	p.baseHost = lang + ".wikipedia.org"
	imgURL, wikiTitle := p.bridge.SearchWikipedia(ctx, query, lang)
	if imgURL != "" {
		pageURL := ""
		if wikiTitle != "" {
			pageURL = fmt.Sprintf("https://%s.wikipedia.org/wiki/%s", lang, strings.ReplaceAll(wikiTitle, " ", "_"))
		}
		return []RetrievalSearchResult{{
			Provider: detail.ProviderWikipedia, Origin: detail.ImageOriginRetrieved,
			PreviewURL: imgURL, PageURL: pageURL, Title: wikiTitle,
			License: "CC-BY-SA-4.0", Author: "Wikipedia Contributors",
		}}, nil
	}
	// Wikimedia Commons is a distinct provider in the shared registry. Keep
	// it there instead of issuing a second Commons request from the Wikipedia
	// adapter for every query that lacks a Wikipedia thumbnail.
	return nil, nil
}

// ID returns the canonical string ID of this provider.
func (p *WikipediaProvider) ID() string { return string(p.Name()) }
