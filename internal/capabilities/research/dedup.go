// Package webresearch — dedup.go provides URL normalization and
// cross-provider deduplication for search hits. When multiple providers
// (SearXNG, DuckDuckGo) return overlapping results, this module ensures
// each unique page appears exactly once in the merged candidate pool.
//
// New adapter (August 2026): registered in package_hotspots.json under
// the infrastructure adapter migration owner for multi-provider research
// fallback.
package research

import (
	"errors"
	"net/url"
	"strings"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

// ErrInvalidURL is the typed sentinel for URLs that cannot be normalized
// (empty input, non-http(s) scheme, or missing host).
var ErrInvalidURL = errors.New("webresearch: invalid URL")

// trackingParams are query parameters stripped during URL normalization.
// These carry marketing attribution, not content identity.
var trackingParams = map[string]struct{}{
	"utm_source": {}, "utm_medium": {}, "utm_campaign": {}, "utm_term": {}, "utm_content": {},
	"fbclid": {}, "gclid": {}, "gclsrc": {}, "dclid": {}, "msclkid": {},
	"ref": {}, "source": {}, "spm": {},
}

// NormalizeWebURL canonicalizes a URL for deduplication: lowercase
// scheme+host, strip www., remove trailing slash, drop tracking query
// params, strip fragment.
func NormalizeWebURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrInvalidURL
	}
	if u.Host == "" {
		return "", ErrInvalidURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Host)
	host = strings.TrimPrefix(host, "www.")
	u.Host = host
	u.Path = strings.TrimSuffix(u.Path, "/")
	if u.Path == "" {
		u.Path = "/"
	}
	q := u.Query()
	for key := range q {
		if _, ok := trackingParams[key]; ok {
			q.Del(key)
		}
	}
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String(), nil
}

// DeduplicateHits removes duplicate search hits by normalized URL.
// Second pass: same host + exactly equal normalized title → keep first.
func DeduplicateHits(hits []scriptports.WebSearchHit) []scriptports.WebSearchHit {
	type entry struct {
		hit   scriptports.WebSearchHit
		norm  string
		host  string
		title string
	}
	seen := make(map[string]struct{}, len(hits))
	var deduped []entry
	for _, h := range hits {
		norm, err := NormalizeWebURL(h.URL)
		if err != nil {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		u, _ := url.Parse(norm)
		host := ""
		if u != nil {
			host = u.Host
		}
		title := normalizeTitle(h.Title)
		deduped = append(deduped, entry{hit: h, norm: norm, host: host, title: title})
	}
	// Second pass: same host + same normalized title → keep first.
	seenTitle := make(map[string]struct{}, len(deduped))
	var result []scriptports.WebSearchHit
	for _, e := range deduped {
		key := e.host + "||" + e.title
		if e.title != "" {
			if _, ok := seenTitle[key]; ok {
				continue
			}
			seenTitle[key] = struct{}{}
		}
		result = append(result, e.hit)
	}
	return result
}

func normalizeTitle(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	title = strings.Join(strings.Fields(title), " ")
	return title
}
