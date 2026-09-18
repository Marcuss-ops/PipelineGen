package client

import (
	"net/http"
	"testing"
	"time"
)

// TestNewClient_TunesTransportForConcurrentFanout pins the transport tuning.
// The Go default (2 idle connections per host) is below every production
// fan-out width, so the third concurrent translation/cue call reopened a TCP
// connection on each request. The wire contract (per-request timeout) is
// unchanged.
func TestNewClient_TunesTransportForConcurrentFanout(t *testing.T) {
	c := NewClient("http://127.0.0.1:11434", "gemma4:e2b", 600)
	if c.httpClient.Timeout != 600*time.Second {
		t.Fatalf("timeout = %s, want 600s (unchanged wire contract)", c.httpClient.Timeout)
	}
	transport, ok := c.httpClient.Transport.(*http.Transport)
	if !ok || transport == nil {
		t.Fatalf("transport = %#v, want a tuned *http.Transport", c.httpClient.Transport)
	}
	if transport.MaxIdleConnsPerHost < 4 {
		t.Fatalf("MaxIdleConnsPerHost = %d, want >= 4 (the widest fan-out is 4)", transport.MaxIdleConnsPerHost)
	}
	if transport.MaxIdleConns < transport.MaxIdleConnsPerHost {
		t.Fatalf("MaxIdleConns = %d must not be below MaxIdleConnsPerHost = %d",
			transport.MaxIdleConns, transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout <= 0 {
		t.Fatalf("IdleConnTimeout = %s, want a positive keep-alive window", transport.IdleConnTimeout)
	}
	if transport.Proxy == nil || transport.DialContext == nil {
		t.Fatal("transport must keep the default dialer/proxy from http.DefaultTransport")
	}
}

// TestNewOllamaHTTPClient_IsNotTheSharedDefaultTransport pins that each client
// owns its transport: mutating one client's pools (or the process default) must
// not silently reconfigure another client.
func TestNewOllamaHTTPClient_IsNotTheSharedDefaultTransport(t *testing.T) {
	first := newOllamaHTTPClient(time.Second)
	second := newOllamaHTTPClient(time.Second)
	if first.Transport == second.Transport {
		t.Fatal("two clients share one transport instance")
	}
	if first.Transport == http.DefaultTransport {
		t.Fatal("client must not use the process-wide http.DefaultTransport")
	}
}
