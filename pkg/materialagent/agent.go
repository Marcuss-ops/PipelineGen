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
