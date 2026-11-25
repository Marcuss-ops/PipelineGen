package client

import (
	"context"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// CircuitBreaker implements a simple circuit breaker for Ollama requests
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // "closed", "open", "half-open"
	failureCount    int
	lastFailureTime time.Time
	maxFailures     int
	timeout         time.Duration
	probeInFlight   bool
	probeDone       chan struct{}
}

func NewCircuitBreaker(maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:       "closed",
		maxFailures: maxFailures,
		timeout:     timeout,
	}
}

func (cb *CircuitBreaker) AllowRequest() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case "closed":
		return true
	case "open":
		if time.Since(cb.lastFailureTime) > cb.timeout {
			cb.state = "half-open"
			cb.probeInFlight = true
			cb.probeDone = make(chan struct{})
			return true
		}
		return false
	case "half-open":
		if cb.probeInFlight {
			return false
		}
		cb.probeInFlight = true
		cb.probeDone = make(chan struct{})
		return true
	}
	return false
}

// AllowRequestContext admits a request or waits for the active half-open
// probe to finish. Waiting callers re-check the state after the probe instead
// of failing the surrounding fan-out while a healthy probe is still running.
func (cb *CircuitBreaker) AllowRequestContext(ctx context.Context) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		cb.mu.Lock()
		switch cb.state {
		case "closed":
			cb.mu.Unlock()
			return true, nil
		case "open":
			if time.Since(cb.lastFailureTime) <= cb.timeout {
				cb.mu.Unlock()
				return false, nil
			}
			cb.state = "half-open"
			cb.probeInFlight = true
			cb.probeDone = make(chan struct{})
			cb.mu.Unlock()
			return true, nil
		case "half-open":
			if !cb.probeInFlight {
				cb.probeInFlight = true
				cb.probeDone = make(chan struct{})
				cb.mu.Unlock()
				return true, nil
			}
			done := cb.probeDone
			cb.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return false, ctx.Err()
			}
		default:
			cb.mu.Unlock()
			return false, nil
		}
	}
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = "closed"
	cb.failureCount = 0
	cb.finishProbeLocked()
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	if cb.state == "half-open" || cb.failureCount >= cb.maxFailures {
		cb.state = "open"
	}
	cb.finishProbeLocked()
}

// RecordCancellation releases a half-open probe that was canceled by its
// caller or a sibling in a shared fan-out. Cancellation is not evidence that
// the model failed, so leave the breaker half-open and do not restart its
// cooldown.
func (cb *CircuitBreaker) RecordCancellation() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	if cb.state == "half-open" && cb.probeInFlight {
		cb.finishProbeLocked()
	}
}

func (cb *CircuitBreaker) finishProbeLocked() {
	cb.probeInFlight = false
	if cb.probeDone != nil {
		close(cb.probeDone)
		cb.probeDone = nil
	}
}

// modelFallbackChains defines fallback model chains
var modelFallbackChains = map[string][]string{
	"qwen2.5:12b":  {"qwen2.5:7b"},
	"llama3.2:12b": {"llama3.2:7b"},
	"mistral:12b":  {"mistral:7b"},
}

// Client client per Ollama API
type Client struct {
	baseURL         string
	httpClient      *http.Client
	model           string
	breakerMu       sync.Mutex
	breakers        map[string]*CircuitBreaker
	useNvidiaForLLM bool
	nvidiaAPIKey    string
	nvidiaLLMModel  string

	useVLLM   bool
	vllmURL   string
	vllmModel string

	webSearcher *WebSearcher

	warmModelGroup singleflight.Group

	// admission is this client's share of the process-wide, per-endpoint
	// in-flight budget for Ollama requests (see admission.go). It is nil for
	// zero-value clients built in tests, and all its methods are nil-safe.
	admission *ollamaAdmission
}

// BaseURL returns the configured Ollama base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Model returns the configured primary model.
func (c *Client) Model() string {
	if c.useVLLM && c.vllmModel != "" {
		return c.vllmModel
	}
	if c.useNvidiaForLLM && c.nvidiaLLMModel != "" {
		return c.nvidiaLLMModel
	}
	return c.model
}

func (c *Client) SetNvidiaConfig(useNvidia bool, apiKey, model string) {
	c.useNvidiaForLLM = useNvidia
	c.nvidiaAPIKey = apiKey
	c.nvidiaLLMModel = model
}

// SetVLLMConfig enables vLLM backend mode (OpenAI-compatible API).
// When useVLLM is true, doChatRequest sends requests to vllmURL/v1/chat/completions
// instead of the Ollama /api/chat endpoint. Mutually exclusive with SetNvidiaConfig
// (vLLM takes precedence if both are set).
func (c *Client) SetVLLMConfig(useVLLM bool, url, model string) {
	c.useVLLM = useVLLM
	c.vllmURL = url
	c.vllmModel = model
	if useVLLM {
		c.useNvidiaForLLM = false
	}
}

// SetWebSearcher enables RAG web search augmentation for Chat calls.
// When set, ChatWithWebContext will search SearXNG before calling the LLM
// and inject the results as context in the user message.
func (c *Client) SetWebSearcher(ws *WebSearcher) {
	c.webSearcher = ws
}

// WebSearcher returns the configured web searcher, or nil if disabled.
func (c *Client) WebSearcher() *WebSearcher {
	return c.webSearcher
}

// ResetCircuitBreakers clears all circuit breakers, allowing requests
// to all models to proceed regardless of previous failure state.
// Returns the number of breakers that were reset.
func (c *Client) ResetCircuitBreakers() int {
	c.breakerMu.Lock()
	defer c.breakerMu.Unlock()
	count := len(c.breakers)
	c.breakers = make(map[string]*CircuitBreaker)
	return count
}

// breakerFor returns a circuit breaker scoped to a specific model.
// Keeping breakers per model prevents a flaky auxiliary model from blocking
// unrelated calls that use the same Ollama client.
func (c *Client) breakerFor(model string) *CircuitBreaker {
	key := model
	if key == "" {
		key = "__default__"
	}

	c.breakerMu.Lock()
	defer c.breakerMu.Unlock()

	if c.breakers == nil {
		c.breakers = make(map[string]*CircuitBreaker)
	}
	if breaker, ok := c.breakers[key]; ok {
		return breaker
	}

	breaker := NewCircuitBreaker(types.CircuitBreakerFailures, types.CircuitBreakerTimeout*time.Second)
	c.breakers[key] = breaker
	return breaker
}
