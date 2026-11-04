package client

import (
	"net"
	"net/http"
	"time"
)

// NewStreamingHTTPClient returns the HTTP client shared by long object and
// queue transfers: there is deliberately NO blanket per-request deadline — the
// body is bounded by the caller's context, and a fixed total timeout (the
// historical shape) killed slow but progressing transfers. Connect, TLS and
// response-header phases are individually bounded so a hung server still fails
// fast. The worker's object-store client lives in another module that already
// depends on this one, so the tuning is exported instead of restated there.
func NewStreamingHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}}
}
