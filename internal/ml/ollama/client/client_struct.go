package client

import (
	"net/http"
	"sync"
	"time"

	"github.com/Marcuss-ops/PipelineGen/internal/ml/ollama/types"
)

// CircuitBreaker implements a simple circuit breaker for Ollama requests
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // "closed", "open", "half-open"
	failureCount    int
	lastFailureTime time.Time
	maxFailures     int
	timeout         time.Duration
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
			return true
		}
		return false
	case "half-open":
		return true
	}
	return false
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = "closed"
	cb.failureCount = 0
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failureCount++
	cb.lastFailureTime = time.Now()
	if cb.failureCount >= cb.maxFailures {
		cb.state = "open"
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
	webSearcher     *WebSearcher
}

// BaseURL returns the configured Ollama base URL.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// Model returns the configured primary model.
func (c *Client) Model() string {
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
