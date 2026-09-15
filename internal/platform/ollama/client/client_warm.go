package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	kernobs "github.com/Marcuss-ops/PipelineGen/internal/kernel/observability"
	"github.com/Marcuss-ops/PipelineGen/internal/platform/ollama/types"
)

// residentRunnerOptions pins an inference request to the single resident
// Ollama runner configuration.
//
// Ollama reconfigures (unload + reload) a resident model whenever the request
// asks for a different context window, so ONE option tuple has to hold across
// every producer in this client: the warm probe, script generation, entity /
// important-phrase extraction and the legacy /api/generate helpers. When a
// call omitted num_ctx, Ollama fell back to its own default (4096) and tore
// down the 8192 runner the warm-up had just paid for — every phrase-extraction
// call then re-loaded the model inside the run it was supposed to accelerate
// (measured: `ollama/warm` 32.4 s of model_load followed by `ollama/generate`
// still reporting cold_start=1, and a single `nlp.extract` at 58.4 s).
//
// An explicit num_ctx stays authoritative: callers that intentionally opt out
// of the resident bucket keep their value, and the caller's map is never
// mutated.
func residentRunnerOptions(options map[string]any) map[string]any {
	if _, ok := options["num_ctx"]; ok {
		return options
	}
	pinned := make(map[string]any, len(options)+1)
	for key, value := range options {
		pinned[key] = value
	}
	pinned["num_ctx"] = types.ProductionRunnerContext
	return pinned
}

// WarmModel makes model residency an explicit, singleflight operation. The
// first caller pays the load; concurrent scene callers wait for that same
// load instead of independently entering Ollama's cold-start window.
//
// The probe is measured as its own canonical operation (ollama/warm) rather
// than being left as un-attributed stage wall. When it was unmeasured, a warm
// probe that itself paid a full model load landed inside the `generate` stage
// wall with no operation behind it, so the stage appeared slow while the
// operations inside it looked fast — and the regression that made the warm-up
// self-defeating was invisible. The merged metadata carries the same facts
// the real generate path records (model_load_ms, cold_start, num_ctx,
// inference_work_ms) so a warm/cold comparison is possible without guessing.
func (c *Client) WarmModel(ctx context.Context, model string) error {
	if c == nil {
		return fmt.Errorf("ollama client is nil")
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = c.model
	}
	if model == "" || c.useVLLM || c.useNvidiaForLLM {
		return nil
	}

	_, err, _ := c.warmModelGroup.Do(model, func() (any, error) {
		const requiredContext int64 = types.ProductionRunnerContext
		resident, checkErr := c.IsModelResidentWithContext(ctx, model, requiredContext)
		if checkErr != nil {
			return nil, fmt.Errorf("verify live residency for %q: %w", model, checkErr)
		}
		if resident {
			// Already warm: no load is paid, so there is nothing to measure.
			// Recording a zero-work operation here would add noise to the
			// report, not information.
			return nil, nil
		}

		// A minimal chat request forces Ollama to load the model while keeping
		// it resident. The real scene fan-out starts only after this returns.
		options := map[string]any{
			// Match the production runner bucket. Ollama reloads/reconfigures a
			// resident model when the context changes, so one fixed bucket keeps
			// cold-start work outside the first production request.
			"model": model, "num_predict": 1, "num_ctx": requiredContext,
		}
		var chatMetrics *ChatMetrics
		info := kernobs.OperationInfo{
			Stage:     kernobs.StageGenerate,
			Component: kernobs.ComponentOllama,
			Operation: kernobs.OperationWarm,
			Items:     1,
		}
		info.OnRecord = func(op *kernobs.OperationReport) {
			if chatMetrics == nil {
				return
			}
			meta := map[string]any{}
			if op.MetadataJSON != "" {
				_ = json.Unmarshal([]byte(op.MetadataJSON), &meta)
			}
			if meta == nil {
				meta = map[string]any{}
			}
			meta["model"] = model
			meta["num_ctx"] = requiredContext
			meta["model_load_ms"] = chatMetrics.ModelLoadMS()
			meta["inference_wall_ms"] = chatMetrics.InferenceWallMS()
			meta["inference_work_ms"] = chatMetrics.InferenceWorkMS()
			meta["cold_start"] = chatMetrics.ColdStart()
			if b, err := json.Marshal(meta); err == nil {
				op.MetadataJSON = string(b)
			}
		}
		probeErr := kernobs.MeasureOperation(ctx, info, func(probeCtx context.Context) error {
			result, chatErr := c.ChatDetailed(probeCtx, []types.Message{{Role: "system", Content: "warmup"}}, options, nil)
			chatMetrics = result.Metrics
			return chatErr
		})
		if probeErr != nil {
			return nil, probeErr
		}
		resident, checkErr = c.IsModelResidentWithContext(ctx, model, requiredContext)
		if checkErr != nil {
			return nil, fmt.Errorf("verify post-warm residency for %q: %w", model, checkErr)
		}
		if !resident {
			return nil, fmt.Errorf("model %q is not resident after warmup", model)
		}
		return nil, nil
	})
	if err != nil {
		return fmt.Errorf("warm Ollama model %q: %w", model, err)
	}
	return nil
}
