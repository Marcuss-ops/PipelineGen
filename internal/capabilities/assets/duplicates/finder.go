// Package duplicates is the canonical capability for asset-level
// duplicate detection (godlike/06 "one owner per fact").
//
// finder.go exposes the DuplicateFinder orchestrator. It fans out a
// content-hash query to every registered Source, merges the results,
// and returns a deterministic slice of DuplicateMatch rows.
package duplicates

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/Marcuss-ops/PipelineGen/pkg/concurrent"
)

// Source is the per-source hash-lookup port consumed by Finder.
// Each source answers FindByHash with the canonical DuplicateMatch
// shape. Implementations are thin adapters over clip repositories
// or provider-specific stores.
type Source interface {
	// Name returns the canonical source identity (e.g. "local",
	// "artlist", "youtube", "stock"). Must be unique within a
	// Finder instance; empty names are rejected at registration.
	Name() string

	// FindByHash returns every asset-row in the source whose
	// content hash exactly matches the given hash. An empty result
	// is a normal case (no duplicates). Idempotent.
	FindByHash(ctx context.Context, hash string) ([]DuplicateMatch, error)
}

// Finder fans out hash lookups across registered Sources.
type Finder struct {
	sources map[string]Source
}

// NewFinder constructs a Finder from an optional list of sources.
// Sources with empty names or duplicate names are ignored.
func NewFinder(sources ...Source) *Finder {
	f := &Finder{sources: make(map[string]Source)}
	for _, s := range sources {
		f.AddSource(s)
	}
	return f
}

// AddSource registers an additional source. If a source with the
// same Name already exists, it is replaced (last-write-wins).
func (f *Finder) AddSource(s Source) {
	if f == nil || s == nil || s.Name() == "" {
		return
	}
	f.sources[s.Name()] = s
}

// Find runs the hash lookup across every registered Source and
// returns the merged, deduplicated list of matches.
//
// Partial-results contract: per-source errors are logged but do not
// abort the fan-out; the successful sources still contribute their
// matches. When every source errors, the returned error aggregates
// the per-source messages.
func (f *Finder) Find(ctx context.Context, hash string) ([]DuplicateMatch, error) {
	if f == nil {
		return nil, fmt.Errorf("duplicates: Finder is nil")
	}
	if hash == "" {
		return []DuplicateMatch{}, nil
	}

	var mu sync.Mutex
	var all []DuplicateMatch
	var errs []error
	var wg sync.WaitGroup

	for _, s := range f.sources {
		wg.Add(1)
		concurrent.SafeGoFunc("duplicates-find", s, func(src Source) {
			defer wg.Done()
			matches, err := src.FindByHash(ctx, hash)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, fmt.Errorf("source %q: %w", src.Name(), err))
				return
			}
			all = append(all, matches...)
		})
	}
	wg.Wait()

	all = deduplicateMatches(all)
	sortMatches(all)

	if len(all) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("duplicates: all sources failed: %v", errs)
	}
	return all, nil
}

func deduplicateMatches(in []DuplicateMatch) []DuplicateMatch {
	seen := make(map[string]struct{}, len(in))
	out := make([]DuplicateMatch, 0, len(in))
	for _, m := range in {
		key := m.Source + "::" + m.AssetID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, m)
	}
	return out
}

func sortMatches(in []DuplicateMatch) {
	sort.Slice(in, func(i, j int) bool {
		if in[i].Source != in[j].Source {
			return in[i].Source < in[j].Source
		}
		if in[i].AssetID != in[j].AssetID {
			return in[i].AssetID < in[j].AssetID
		}
		return in[i].Name < in[j].Name
	})
}
