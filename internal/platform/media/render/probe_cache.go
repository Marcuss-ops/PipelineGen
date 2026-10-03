package render

import (
	"context"
	"os"
	"sync"

	stockpipeline "github.com/Marcuss-ops/PipelineGen/internal/capabilities/assets/providers/stock/stockpipeline"
)

// Compile-time check that CachedSourceDurationProbe satisfies the port.
var _ stockpipeline.SourceDurationProbe = (*CachedSourceDurationProbe)(nil)

// cachedProbeMaxEntries bounds the identity cache so a long-lived server that
// stages thousands of distinct source files cannot grow the map without
// bound. On overflow the cache resets: the next probes re-measure and repopulate.
const cachedProbeMaxEntries = 4096

// probeIdentity is the cache key: a duration is a pure function of the file
// bytes, and (path, size, mtime) identifies the bytes cheaply. Same identity
// → same duration, so the cache needs no TTL; a re-encoded or re-staged file
// gets a new mtime/size and measures fresh.
type probeIdentity struct {
	path      string
	sizeBytes int64
	mtimeNano int64
}

// CachedSourceDurationProbe wraps any SourceDurationProbe with an identity-
// keyed memo of successful measurements. It exists because the timing
// snapshot records `stock.duration_probe` at 25.7s average (max 93.5s, 4.1h
// total) and retries/fan-outs re-probe the SAME staged file within one job —
// ffprobe on a multi-GB source is pure repeated work when the identity has
// not changed.
//
// Only successes are cached: a failed probe may be transient (file still
// being staged) and must retry against the underlying probe. A stat failure
// bypasses the cache entirely and defers the real error to the probe.
type CachedSourceDurationProbe struct {
	underlying stockpipeline.SourceDurationProbe

	mu      sync.Mutex
	cache   map[probeIdentity]float64
	flights map[probeIdentity]*durationProbeFlight
}

// Each caller owns a wait, not the shared probe's lifetime. One cancelled
// caller cannot cancel another; when the last waiter leaves the process is
// cancelled and a later caller may start a fresh attempt.
type durationProbeFlight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	duration float64
	err      error
}

// NewCachedSourceDurationProbe wraps the given probe with the identity cache.
func NewCachedSourceDurationProbe(underlying stockpipeline.SourceDurationProbe) *CachedSourceDurationProbe {
	return &CachedSourceDurationProbe{
		underlying: underlying,
		cache:      make(map[probeIdentity]float64),
		flights:    make(map[probeIdentity]*durationProbeFlight),
	}
}

// ProbeDurationSec implements stockpipeline.SourceDurationProbe.
func (p *CachedSourceDurationProbe) ProbeDurationSec(ctx context.Context, sourcePath string) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	identity, statOK := probeFileIdentity(sourcePath)
	if !statOK {
		return p.underlying.ProbeDurationSec(ctx, sourcePath)
	}

	p.mu.Lock()
	if cached, ok := p.cache[identity]; ok {
		p.mu.Unlock()
		return cached, nil
	}
	flight := p.flights[identity]
	if flight == nil {
		probeCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		flight = &durationProbeFlight{done: make(chan struct{}), cancel: cancel}
		p.flights[identity] = flight
		go p.measureDuration(probeCtx, sourcePath, identity, flight)
	}
	flight.waiters++
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		flight.waiters--
		if flight.waiters == 0 {
			flight.cancel()
			if p.flights[identity] == flight {
				delete(p.flights, identity)
			}
		}
		p.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-flight.done:
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return flight.duration, flight.err
	}
}

func (p *CachedSourceDurationProbe) measureDuration(ctx context.Context, path string, identity probeIdentity, flight *durationProbeFlight) {
	defer flight.cancel()
	duration, err := p.underlying.ProbeDurationSec(ctx, path)
	current, statOK := probeFileIdentity(path)
	p.mu.Lock()
	defer p.mu.Unlock()
	flight.duration, flight.err = duration, err
	// Never memoize a cancelled/orphaned attempt or bytes that changed while
	// ffprobe ran. An older flight must not replace a newer attempt's result.
	if p.flights[identity] == flight {
		delete(p.flights, identity)
		if err == nil && ctx.Err() == nil && statOK && current == identity {
			if len(p.cache) >= cachedProbeMaxEntries {
				p.cache = make(map[probeIdentity]float64)
			}
			p.cache[identity] = duration
		}
	}
	close(flight.done)
}

// probeFileIdentity stats the path; ok=false means the identity is unknown
// (missing file or unreadable metadata) and the caller must bypass the cache.
func probeFileIdentity(path string) (probeIdentity, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return probeIdentity{}, false
	}
	return probeIdentity{
		path:      path,
		sizeBytes: info.Size(),
		mtimeNano: info.ModTime().UnixNano(),
	}, err == nil
}
