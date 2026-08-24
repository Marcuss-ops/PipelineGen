package research

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	scriptports "github.com/Marcuss-ops/PipelineGen/internal/capabilities/scripts/ports"
)

type PageFetcher struct {
	client   *http.Client
	maxBytes int
}

func NewPageFetcher(timeout time.Duration, maxBytes int) *PageFetcher {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if maxBytes <= 0 {
		maxBytes = 2 << 20
	}
	f := &PageFetcher{maxBytes: maxBytes}
	f.client = &http.Client{Timeout: timeout, CheckRedirect: func(req *http.Request, _ []*http.Request) error {
		if err := validatePublicURL(req.URL); err != nil {
			return err
		}
		return nil
	}}
	return f
}

func (f *PageFetcher) Fetch(ctx context.Context, rawURL string, maxChars int) (scriptports.WebPage, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return scriptports.WebPage{}, fmt.Errorf("web page url: %w", err)
	}
	if err := validatePublicURL(u); err != nil {
		return scriptports.WebPage{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return scriptports.WebPage{}, err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("User-Agent", "PipelineGen-WebResearch/1.0 (+https://pipelinegen.local/research)")
	resp, err := f.client.Do(req)
	if err != nil {
		return scriptports.WebPage{}, fmt.Errorf("page fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return scriptports.WebPage{}, fmt.Errorf("page fetch returned status %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); ct != "" && !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		return scriptports.WebPage{}, fmt.Errorf("page fetch: unsupported content type %q", ct)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(f.maxBytes)+1))
	if err != nil {
		return scriptports.WebPage{}, err
	}
	if len(body) > f.maxBytes {
		return scriptports.WebPage{}, fmt.Errorf("page fetch: response too large")
	}
	text, title := extractHTML(body, maxChars)
	return scriptports.WebPage{URL: u.String(), Title: title, Text: text}, nil
}

func validatePublicURL(u *url.URL) error {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("web page url blocked: only public http(s) URLs are allowed")
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return fmt.Errorf("web page url blocked: local host")
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("web page url: resolve host: %w", err)
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("web page url blocked: private address %s", ip)
		}
	}
	return nil
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
}

func extractHTML(body []byte, maxChars int) (string, string) {
	raw := string(body)
	title := ""
	if m := regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`).FindStringSubmatch(raw); len(m) == 2 {
		title = trimHTML(m[1], 0)
	}
	raw = regexp.MustCompile(`(?is)<(?:script|style|nav|footer|header)[^>]*>.*?</(?:script|style|nav|footer|header)>`).ReplaceAllString(raw, " ")
	raw = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(raw, " ")
	return trimHTML(raw, maxChars), title
}
func trimHTML(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if n > 0 && len(s) > n {
		return s[:n] + "..."
	}
	return s
}
