package materialagent

import (
	"sort"
	"strings"
)

// ── Registry ────────────────────────────────────────────────────────────────

// Registry holds the available resolvers by stable name. It is the ONLY place
// source names are bound to implementations, so the orchestrator never grows
// an if-chain over "youtube"/"stock"/"catalog".
type Registry struct {
	resolvers map[string]Resolver
	order     []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{resolvers: map[string]Resolver{}}
}

// Register adds (or replaces) a resolver. Registration order is retained for
// callers that want the raw insertion order.
func (r *Registry) Register(res Resolver) {
	if r == nil || res == nil {
		return
	}
	name := strings.TrimSpace(res.Name())
	if name == "" {
		return
	}
	if r.resolvers == nil {
		r.resolvers = map[string]Resolver{}
	}
	if _, exists := r.resolvers[name]; !exists {
		r.order = append(r.order, name)
	}
	r.resolvers[name] = res
}

// Get returns the resolver registered under name.
func (r *Registry) Get(name string) (Resolver, bool) {
	if r == nil {
		return nil, false
	}
	res, ok := r.resolvers[strings.TrimSpace(name)]
	return res, ok
}

// Names returns the insertion-ordered resolver names.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// ── Policy ──────────────────────────────────────────────────────────────────

// Resolver names the policy and registry keys are shared under.
const (
	ResolverCatalog = "catalog"
	ResolverYouTube = "youtube"
	ResolverStock   = "stock"
	ResolverImport  = "import"
)

// Policy decides WHICH resolvers a request may use and in what order. The
// ordering is the autonomy rule the platform wants: consult the material we
// already own before acquiring anything new.
type Policy struct {
	// Order is the default resolver order (catalog first).
	Order []string
	// MaxPerResolver caps how many candidates a single resolver may
	// materialize for one request (guards a runaway fallback).
	MaxPerResolver int
}

// DefaultPolicy is catalog → live YouTube discovery → stock acquisition. The
// catalog is first on purpose: if the Master already has good material, nothing
// is downloaded again.
func DefaultPolicy() Policy {
	return Policy{
		Order:          []string{ResolverCatalog, ResolverYouTube, ResolverStock},
		MaxPerResolver: 5,
	}
}

// Plan resolves the ordered resolver names for a request: an explicit
// req.Sources wins (caller override), otherwise the policy order. Unknown names
// are preserved — the orchestrator skips missing resolvers rather than
// silently dropping a caller's intent.
func (p Policy) Plan(req MaterialRequest) []string {
	if len(req.Sources) > 0 {
		out := make([]string, 0, len(req.Sources))
		for _, s := range req.Sources {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return append([]string(nil), p.Order...)
}

// ── Scorer ──────────────────────────────────────────────────────────────────

// Scorer ranks candidates for a request. It is intentionally small: relevance
// comes from the resolver, and the scorer only adjusts for the things a
// resolver cannot know (does the duration fit the scene, are the bytes already
// durable, is the resolution good enough).
type Scorer struct {
	RelevanceWeight     float64
	DurationFitBonus    float64
	DriveBonus          float64
	LocalBonus          float64
	QualityBonus        float64
	DurationMissPenalty float64
	// MinWidth is the pixel-width that earns QualityBonus (0 = disabled).
	MinWidth int
}

// DefaultScorer returns the calibrated default (relevance dominates; a
// duration that fits is a meaningful tiebreaker; already-durable bytes win over
// a download).
func DefaultScorer() Scorer {
	return Scorer{
		RelevanceWeight:     1.0,
		DurationFitBonus:    0.10,
		DriveBonus:          0.05,
		LocalBonus:          0.03,
		QualityBonus:        0.05,
		DurationMissPenalty: 0.25,
		MinWidth:            1920,
	}
}

// Score returns the ranking score for c against req. Higher is better.
func (s Scorer) Score(c Candidate, req MaterialRequest) float64 {
	score := s.RelevanceWeight * c.Relevance
	if c.HasDrive {
		score += s.DriveBonus
	}
	if c.HasLocal {
		score += s.LocalBonus
	}
	if s.MinWidth > 0 && c.Width >= s.MinWidth {
		score += s.QualityBonus
	}

	lo := req.Constraints.DurationMinSeconds
	hi := req.Constraints.DurationMaxSeconds
	if (lo > 0 || hi > 0) && c.DurationSeconds > 0 {
		fits := (lo == 0 || c.DurationSeconds >= lo) && (hi == 0 || c.DurationSeconds <= hi)
		if fits {
			score += s.DurationFitBonus
		} else {
			score -= s.DurationMissPenalty
		}
	}
	return score
}

// Rank returns the candidates ordered best-first. The sort is stable, so two
// equally-scored candidates keep the resolver's own order (which encodes the
// server-side ranking).
func (s Scorer) Rank(candidates []Candidate, req MaterialRequest) []Candidate {
	ranked := make([]Candidate, len(candidates))
	copy(ranked, candidates)
	sort.SliceStable(ranked, func(i, j int) bool {
		return s.Score(ranked[i], req) > s.Score(ranked[j], req)
	})
	return ranked
}
