package materialagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/Marcuss-ops/PipelineGen/pkg/veloxclient"
)

// Agent is the autonomous material layer: it resolves a MaterialRequest to real
// material by walking the policy's resolver order, scoring candidates and
// materializing the winners.
//
// The loop is deliberately simple and observable: try each source in order,
// stop as soon as the request's Count is met, and report EVERY per-resolver
// failure (errors.Join) instead of swallowing them — an autonomous run that
// silently degraded is worse than one that reports "catalog: empty, youtube:
// quota, stock: unavailable".
type Agent struct {
	client   *veloxclient.Client
	registry *Registry
	policy   Policy
	scorer   Scorer
}

// Option configures an Agent.
type Option func(*Agent)

// WithPolicy overrides the default resolution policy.
func WithPolicy(p Policy) Option {
	return func(a *Agent) {
		if len(p.Order) > 0 {
			a.policy = p
		}
	}
}

// WithScorer overrides the default candidate scorer.
func WithScorer(s Scorer) Option {
	return func(a *Agent) { a.scorer = s }
}

// New builds an Agent over the given client and registry.
func New(client *veloxclient.Client, registry *Registry, opts ...Option) *Agent {
	a := &Agent{
		client:   client,
		registry: registry,
		policy:   DefaultPolicy(),
		scorer:   DefaultScorer(),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// NewDefaultAgent wires the standard registry (catalog → youtube → stock) over
// one client. It is the one-liner a remote process uses at boot.
func NewDefaultAgent(client *veloxclient.Client, opts ...Option) *Agent {
	reg := NewRegistry()
	reg.Register(NewCatalogResolver(client))
	reg.Register(NewYouTubeResolver(client))
	reg.Register(NewStockResolver(client, 1))
	return New(client, reg, opts...)
}

// Resolve returns up to req.Count pieces of material. It returns
// ErrNoMaterial (joined with each resolver's error) when nothing could be
// produced; partial success is returned with a nil error.
func (a *Agent) Resolve(ctx context.Context, req MaterialRequest) ([]Material, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("materialagent: agent is not wired")
	}
	if req.Count <= 0 {
		req.Count = 1
	}

	var (
		out  []Material
		errs []error
	)
	for _, name := range a.policy.Plan(req) {
		if len(out) >= req.Count {
			break
		}
		resolver, ok := a.registry.Get(name)
		if !ok {
			errs = append(errs, fmt.Errorf("resolver %q requested by policy but not registered", name))
			continue
		}

		candidates, err := resolver.Search(ctx, req)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s search: %w", name, err))
			continue
		}
		if len(candidates) == 0 {
			errs = append(errs, fmt.Errorf("%s: no candidates", name))
			continue
		}

		ranked := a.scorer.Rank(candidates, req)
		if req.Constraints.RequireCaptions {
			budget := a.policy.MaxMetadataProbes
			if budget <= 0 {
				budget = DefaultMaxMetadataProbes
			}
			kept, dropped := a.applyCaptionGate(ctx, resolver, ranked, &budget)
			ranked = kept
			if dropped > 0 {
				// A scene that required captions and found only caption-less
				// candidates is a real outcome: record it (classified) instead
				// of degrading silently.
				errs = append(errs, fmt.Errorf("%s: %d candidate(s) rejected: %w", name, dropped, ErrNoCaptions))
			}
			if len(ranked) == 0 {
				continue
			}
		}
		perResolver := a.policy.MaxPerResolver
		if perResolver <= 0 || perResolver > len(ranked) {
			perResolver = len(ranked)
		}
		for _, c := range ranked[:perResolver] {
			if len(out) >= req.Count {
				break
			}
			m, err := resolver.Materialize(ctx, c, req)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s materialize: %w", name, err))
				continue
			}
			if m == nil {
				continue
			}
			out = append(out, *m)
		}
	}

	if len(out) == 0 {
		return nil, errors.Join(append([]error{ErrNoMaterial}, errs...)...)
	}
	return out, nil
}

// captionProber is an OPTIONAL Resolver capability: a resolver whose candidates
// can be probed for caption availability (live YouTube). Resolvers that do not
// implement it are exempt from the caption gate — reading the catalog or
// acquiring stock must not pay a probe per candidate.
type captionProber interface {
	// ProbeCaptions reports whether candidate c exposes captions. known is
	// false when the probe could not establish it (leaving the caller to apply
	// its fail-closed policy).
	ProbeCaptions(ctx context.Context, c Candidate) (known, has bool)
}

// applyCaptionGate filters ranked candidates against the RequireCaptions gate
// using the resolver's caption probe and spending at most *budget probes.
//
// Semantics (fail-closed): a candidate known to expose no captions is dropped;
// a candidate whose captions cannot be established — probe error, or the
// budget is already spent — is dropped too. Only candidates known to HAVE
// captions survive. Resolvers without a captionProber pass through untouched.
// It returns the survivors and how many were dropped.
func (a *Agent) applyCaptionGate(ctx context.Context, res Resolver, ranked []Candidate, budget *int) ([]Candidate, int) {
	prober, ok := res.(captionProber)
	if !ok {
		return ranked, 0
	}
	survivors := make([]Candidate, 0, len(ranked))
	dropped := 0
	for _, c := range ranked {
		// A candidate already answered by the search arm needs no probe.
		if !c.CaptionsKnown && *budget > 0 {
			*budget--
			known, has := prober.ProbeCaptions(ctx, c)
			if known {
				c.CaptionsKnown = true
				c.HasCaptions = has
			}
		}
		if c.CaptionsKnown && c.HasCaptions {
			survivors = append(survivors, c)
			continue
		}
		dropped++
	}
	return survivors, dropped
}

// ImportFile registers material the remote produced or acquired on its own
// disk through POST /api/media/clips/upload-video. This is the ONLY way an
// agent adds bytes the Master has never seen — it never touches the database
// directly.
func (a *Agent) ImportFile(ctx context.Context, req MaterialRequest, meta veloxclient.VideoUploadMeta, file io.Reader) (*Material, error) {
	if a == nil || a.client == nil {
		return nil, fmt.Errorf("materialagent: agent is not wired")
	}
	if meta.Description == "" {
		meta.Description = req.Description
	}
	if meta.Source == "" {
		meta.Source = "material-agent"
	}
	raw, err := a.client.UploadVideoClip(ctx, meta, file)
	if err != nil {
		return nil, fmt.Errorf("materialagent: import file: %w", err)
	}
	var resp struct {
		OK          bool   `json:"ok"`
		ClipID      string `json:"clip_id"`
		Name        string `json:"name"`
		DriveLink   string `json:"drive_link"`
		DriveFileID string `json:"drive_file_id"`
		Source      string `json:"source"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("materialagent: import file: decode response: %w", err)
	}
	return &Material{
		Candidate: Candidate{
			Resolver:    ResolverImport,
			AssetID:     resp.ClipID,
			Source:      resp.Source,
			Title:       resp.Name,
			Name:        resp.Name,
			MediaType:   req.MaterialType,
			DriveFileID: resp.DriveFileID,
			HasDrive:    resp.DriveFileID != "",
			SourceURL:   resp.DriveLink,
		},
		Registered: resp.OK && resp.ClipID != "",
	}, nil
}
