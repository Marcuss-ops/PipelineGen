package client

import (
	"net/http"
	"sync"
	"time"
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
	"qwen2.5:12b":  {"qwen2.5:7b", "gemma3:4b"},
	"llama3.2:12b": {"llama3.2:7b", "gemma3:4b"},
	"mistral:12b":  {"mistral:7b", "gemma3:4b"},
	"gemma3:12b":   {"gemma3:4b"},
	"qwen2.5:7b":   {"gemma3:4b"},
	"llama3.2:7b":  {"gemma3:4b"},
}

// Client client per Ollama API
type Client struct {
	baseURL        string
	httpClient     *http.Client
	model          string
	circuitBreaker *CircuitBreaker
}
