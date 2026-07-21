// Package mediamemory — registry_test.go pins the canonical
// freezing + locking contract of StrategyRegistry.
//
// godlike/06 SSOT (mirror of search.BackendRegistry): the
// StrategyRegistry's typed-sentinel envelope (ErrNilStrategy,
// ErrEmptyName, ErrFrozen, ErrAlreadyRegistered,
// ErrStrategyNotFound) is the canonical contract that production
// composition-root adapters satisfy. Drift here is a contract
// break that propagates to every composition site.
//
// godlike/07 NO-FAKE-AVAILABILITY: the typed-sentinel tests
// verify errors.Is returning true for wrapped miss/duplicate
// cases. A renamed sentinel would silently break the resolver's
// error classification.
package mediamemory

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubStrategy is a minimal Strategy impl used by registry tests.
type stubStrategy struct {
	name    string
	concept MediaConcept
}

func (s *stubStrategy) Name() string { return s.name }
func (s *stubStrategy) ConceptTypes() []ConceptType {
	return []ConceptType{ConceptPhrase}
}
func (s *stubStrategy) ResolvesConcept(c MediaConcept) bool {
	return c.ConceptType == ConceptPhrase
}

func TestStrategyRegistryRegisterAndLookup(t *testing.T) {
	r := NewStrategyRegistry()
	require.NoError(t, r.Register(&stubStrategy{name: "alpha"}))
	s, err := r.Lookup("alpha")
	require.NoError(t, err)
	assert.Equal(t, "alpha", s.Name())
}

func TestStrategyRegistryRegisterNilReturnsErrNilStrategy(t *testing.T) {
	r := NewStrategyRegistry()
	err := r.Register(nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNilStrategy),
		"nil interface strategy MUST be rejected as ErrNilStrategy")
}

func TestStrategyRegistryRegisterTypedNilPointerReturnsErrNilStrategy(t *testing.T) {
	r := NewStrategyRegistry()
	var s *stubStrategy = nil
	err := r.Register(s)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrNilStrategy),
		"typed-nil pointer strategy MUST be rejected as ErrNilStrategy")
}

func TestStrategyRegistryRegisterEmptyNameReturnsErrEmptyName(t *testing.T) {
	r := NewStrategyRegistry()
	err := r.Register(&stubStrategy{name: ""})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEmptyName),
		"empty Name() MUST be rejected as ErrEmptyName")
}

func TestStrategyRegistryRegisterDuplicateReturnsErrAlreadyRegistered(t *testing.T) {
	r := NewStrategyRegistry()
	require.NoError(t, r.Register(&stubStrategy{name: "alpha"}))
	err := r.Register(&stubStrategy{name: "alpha"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrAlreadyRegistered),
		"duplicate name MUST be rejected as ErrAlreadyRegistered")
}

func TestStrategyRegistryFreezeBlocksFurtherRegister(t *testing.T) {
	r := NewStrategyRegistry()
	require.NoError(t, r.Register(&stubStrategy{name: "alpha"}))
	r.Freeze()
	assert.True(t, r.IsFrozen(), "IsFrozen MUST be true after Freeze")
	err := r.Register(&stubStrategy{name: "beta"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrFrozen),
		"post-Freeze Register MUST be rejected as ErrFrozen")
}

func TestStrategyRegistryFreezeIsIdempotent(t *testing.T) {
	r := NewStrategyRegistry()
	r.Freeze()
	r.Freeze() // second Freeze MUST NOT panic or toggle non-frozen
	assert.True(t, r.IsFrozen())
}

func TestStrategyRegistryAllReturnsSortedByName(t *testing.T) {
	r := NewStrategyRegistry()
	for _, name := range []string{"gamma", "alpha", "beta"} {
		require.NoError(t, r.Register(&stubStrategy{name: name}))
	}
	got := r.All()
	require.Len(t, got, 3)
	names := []string{got[0].Name(), got[1].Name(), got[2].Name()}
	assert.Equal(t,
		[]string{"alpha", "beta", "gamma"}, names,
		"All() MUST sort by Name for deterministic iteration",
	)
}

func TestStrategyRegistryLookupUnknownReturnsErrStrategyNotFound(t *testing.T) {
	r := NewStrategyRegistry()
	_, err := r.Lookup("missing")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrStrategyNotFound),
		"unknown-key Lookup MUST be rejected as ErrStrategyNotFound")
}

func TestStrategyRegistryEligibleForFiltersByConcept(t *testing.T) {
	r := NewStrategyRegistry()
	require.NoError(t, r.Register(&stubStrategy{name: "phrase-only"}))
	got := r.EligibleFor(MediaConcept{ConceptType: ConceptPhrase})
	require.Len(t, got, 1)
	assert.Equal(t, "phrase-only", got[0].Name())

	// stubStrategy.ResolvesConcept only returns true for ConceptPhrase;
	// a different ConceptType yields zero strategies.
	got2 := r.EligibleFor(MediaConcept{ConceptType: ConceptEntity})
	assert.Empty(t, got2, "EligibleFor MUST return zero strategies for non-matching concept type")
}

// TestStrategyRegistryConcurrentRegister explodes 50 goroutines
// trying to Register. With mu.Lock() in Register(), no duplicate
// registration can succeed; the registry stays consistent.
//
// godlike/06 SSOT (lock witness): a race detector run on this
// test (go test -race) would surface any unprotected access.
func TestStrategyRegistryConcurrentRegister(t *testing.T) {
	r := NewStrategyRegistry()
	var wg sync.WaitGroup
	errorsCh := make(chan error, 100)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "strat-" + string(rune('a'+i%26)) + "-" // 26 names; ~24 collisions expected
			err := r.Register(&stubStrategy{name: name})
			if err != nil {
				errorsCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	count := 0
	for err := range errorsCh {
		// ErrAlreadyRegistered is the expected wrong-shape outcome
		// (50 goroutines, 26 names → at least ~24 dups).
		if !errors.Is(err, ErrAlreadyRegistered) {
			t.Errorf("unexpected error type: %v", err)
		}
		count++
	}
	assert.GreaterOrEqual(t, count, 1,
		"with 50 goroutines and 26 names there MUST be at least 1 ErrAlreadyRegistered")
}

// TestStrategyRegistryFreezeConcurrentWithLookup: freezes and
// lookups must coexist. (Locking contract: Freeze uses mu.Lock
// (write), Lookup uses mu.RLock — the cont/write race is benign.)
func TestStrategyRegistryFreezeConcurrentWithLookup(t *testing.T) {
	r := NewStrategyRegistry()
	require.NoError(t, r.Register(&stubStrategy{name: "alpha"}))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Freeze()
		}()
		go func() {
			defer wg.Done()
			_, _ = r.Lookup("alpha")
		}()
	}
	wg.Wait()
	assert.True(t, r.IsFrozen())
}
