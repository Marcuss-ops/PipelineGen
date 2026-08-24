package assembly

import (
	"context"
	"encoding/json"
	"fmt"

	appjobs "github.com/Marcuss-ops/PipelineGen/internal/capabilities/jobs/queue"
	contract "github.com/Marcuss-ops/PipelineGen/internal/kernel/assembly"
	job "github.com/Marcuss-ops/PipelineGen/internal/kernel/job"
)

// AssetPreparer is implemented by the Velox filesystem/cache adapter. It
// must be idempotent and verify SHA256 before returning ready=true.
type AssetPreparer interface {
	Prepare(context.Context, contract.AssetRequirement) (ready bool, err error)
}

// Finalizer owns ffprobe validation, concat demuxer creation, -c copy, and
// artifact publication. Keeping it behind this port makes the job handler
// testable without making the contract package aware of ffmpeg or storage.
type Finalizer interface {
	Finalize(context.Context, contract.FinalizeV1) (contract.FinalizeResultV1, error)
}

type Handler struct {
	preparer  AssetPreparer
	finalizer Finalizer
}

func NewHandler(preparer AssetPreparer, finalizer Finalizer) (*Handler, error) {
	if preparer == nil || finalizer == nil {
		return nil, fmt.Errorf("assembly handler requires preparer and finalizer")
	}
	return &Handler{preparer: preparer, finalizer: finalizer}, nil
}

type Broker interface{ RegisterHandler(string, any) error }

func (h *Handler) Register(s Broker) error {
	if s == nil {
		return fmt.Errorf("assembly handler: jobs service is nil")
	}
	if err := s.RegisterHandler(contract.PrepareJobType, appjobs.HandlerFunc(h.Prepare)); err != nil {
		return err
	}
	return s.RegisterHandler(contract.FinalizeJobType, appjobs.HandlerFunc(h.Finalize))
}

func (h *Handler) Prepare(ctx context.Context, j *job.Job, tools *job.JobExecutionTools) (map[string]any, error) {
	var p contract.PrepareV1
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return nil, fmt.Errorf("assembly.prepare decode: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	ready := 0
	missing := 0
	for _, a := range p.Assets {
		ok, err := h.preparer.Prepare(ctx, a)
		if err != nil {
			return nil, fmt.Errorf("prepare %s: %w", a.AssetID, err)
		}
		if ok {
			ready++
		} else if a.Required {
			missing++
		}
	}
	state := "prepared"
	if missing > 0 {
		return nil, fmt.Errorf("assembly.prepare: %d required assets are not ready", missing)
	}
	result := contract.PrepareResultV1{ContractVersion: contract.ContractVersion, AssemblyID: p.AssemblyID, PreparationID: p.PreparationHash, PreparationHash: p.PreparationHash, AssetsReady: ready, AssetsMissing: missing, State: state}
	b, _ := json.Marshal(result)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out, nil
}

func (h *Handler) Finalize(ctx context.Context, j *job.Job, _ *job.JobExecutionTools) (map[string]any, error) {
	var p contract.FinalizeV1
	if err := json.Unmarshal(j.Payload, &p); err != nil {
		return nil, fmt.Errorf("assembly.finalize decode: %w", err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	r, err := h.finalizer.Finalize(ctx, p)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(r)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if r.ArtifactPath != "" {
		manifest, err := ArtifactManifestResult(j.ID, r.ArtifactPath)
		if err != nil {
			return nil, err
		}
		out[job.ManifestKey] = manifest[job.ManifestKey]
	}
	return out, nil
}
