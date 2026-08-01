package ports

import "context"

type WebSearchHit struct {
	Title, URL, Content string
}

type WebSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]WebSearchHit, error)
}

type WebPage struct {
	URL, Title, Publisher, PublishedAt, Text string
}

type WebPageFetcher interface {
	Fetch(ctx context.Context, rawURL string, maxChars int) (WebPage, error)
}
