package channelmonitor

import (
	"sync"
	"time"

	"velox/go-master/pkg/logger"

	"go.uber.org/zap"
)

type CircuitState int

const (
	StateClosed CircuitState = iota
	StateHalfOpen
	StateOpen
)

func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateHalfOpen:
		return "half-open"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

type CircuitBreaker struct {
	mu         sync.RWMutex
	name      string
	state     CircuitState
	failures  int
	successes int
	lastFailure time.Time

	threshold      int
	resetTimeout   time.Duration
	halfOpenMax   int
	halfOpenCount  int
}

type CircuitBreakerSettings struct {
	Name         string
	Threshold    int
	ResetTimeout time.Duration
	HalfOpenMax int
}

func NewCircuitBreaker(s CircuitBreakerSettings) *CircuitBreaker {
	if s.Threshold == 0 {
		s.Threshold = 5
	}
	if s.ResetTimeout == 0 {
		s.ResetTimeout = 10 * time.Minute
	}
	if s.HalfOpenMax == 0 {
		s.HalfOpenMax = 3
	}
	return &CircuitBreaker{
		name:         s.Name,
		state:       StateClosed,
		threshold:   s.Threshold,
		resetTimeout: s.ResetTimeout,
		halfOpenMax:  s.HalfOpenMax,
	}
}

func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return true

	case StateOpen:
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = StateHalfOpen
			cb.halfOpenCount = 0
			logger.Info("Circuit breaker half-open",
				zap.String("name", cb.name),
			)
			return true
		}
		return false

	case StateHalfOpen:
		if cb.halfOpenCount < cb.halfOpenMax {
			cb.halfOpenCount++
			return true
		}
		return false
	}

	return false
}

func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		cb.failures = 0

	case StateHalfOpen:
		cb.successes++
		if cb.successes >= cb.halfOpenMax {
			cb.state = StateClosed
			cb.failures = 0
			cb.successes = 0
			logger.Info("Circuit breaker closed (recovery)",
				zap.String("name", cb.name),
			)
		}
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.lastFailure = time.Now()

	switch cb.state {
	case StateClosed:
		cb.failures++
		if cb.failures >= cb.threshold {
			cb.state = StateOpen
			logger.Warn("Circuit breaker opened",
				zap.String("name", cb.name),
				zap.Int("failures", cb.failures),
			)
		}

	case StateHalfOpen:
		cb.state = StateOpen
		cb.halfOpenCount = 0
		logger.Warn("Circuit breaker reopened",
			zap.String("name", cb.name),
		)
	}
}

func (cb *CircuitBreaker) Counts() (failures, successes int) {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures, cb.successes
}

func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = StateClosed
	cb.failures = 0
	cb.successes = 0
	cb.halfOpenCount = 0
}

type CircuitBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[string]*CircuitBreaker
}

var globalCircuitBreakers = &CircuitBreakerRegistry{
	breakers: make(map[string]*CircuitBreaker),
}

func GetCircuitBreaker(name string) *CircuitBreaker {
	globalCircuitBreakers.mu.RLock()
	if cb, ok := globalCircuitBreakers.breakers[name]; ok {
		globalCircuitBreakers.mu.RUnlock()
		return cb
	}
	globalCircuitBreakers.mu.RUnlock()

	globalCircuitBreakers.mu.Lock()
	defer globalCircuitBreakers.mu.Unlock()

	if cb, ok := globalCircuitBreakers.breakers[name]; ok {
		return cb
	}

	cb := NewCircuitBreaker(CircuitBreakerSettings{
		Name:         name,
		Threshold:    5,
		ResetTimeout: 10 * time.Minute,
		HalfOpenMax:  3,
	})
	globalCircuitBreakers.breakers[name] = cb
	return cb
}